// Package transcode turns uploaded videos (upload.completed.v1) into HLS VODs under videos/{vodId}/.
//
// Live streams always come first: a job starts only while live sessions leave room under
// MEDIA_MAX_CONCURRENT_STREAMS, live admission never counts jobs, and ffmpeg runs at a lower priority.
package transcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
)

const (
	// Durable is the JetStream consumer of upload.completed.v1.
	Durable = "media-transcode"
	// AckWait and MaxDeliver of the durable; a running job calls InProgress well within AckWait.
	AckWait    = 2 * time.Minute
	MaxDeliver = 3

	_uploadsDir   = "uploads"
	_dirPerm      = 0o750
	_filePerm     = 0o640
	_fetchWait    = 5 * time.Second
	_liveFullWait = 5 * time.Second
	_fetchBackoff = 5 * time.Second
	// _liveFullDelay redelivers a job fetched while live streams took every slot.
	_liveFullDelay = 30 * time.Second
	// _shutdownDelay redelivers a job interrupted by a shutdown soon after the restart.
	_shutdownDelay = 5 * time.Second
)

// Delivery acknowledges one fetched event (eventbus.Message).
type Delivery interface {
	Ack() error
	Term() error
	NakWithDelay(delay time.Duration) error
	InProgress() error
}

// Message is one fetched upload.completed.v1.
type Message struct {
	Event eventbus.Event
	// Delivered counts this delivery (1 = first).
	Delivered uint64
	// Seq is the event's stream sequence: unique per published upload.completed.v1.
	Seq      uint64
	Delivery Delivery
}

// Queue hands out one event at a time; Next returns eventbus.ErrNoEvent when none arrived within maxWait.
type Queue interface {
	Next(ctx context.Context, maxWait time.Duration) (Message, error)
}

// LiveSessions counts live streams that hold a transcoding slot (session.Manager).
type LiveSessions interface {
	Active() int
}

// Source streams an uploaded object into a local file (storage.MinIO).
type Source interface {
	Download(ctx context.Context, bucket, key, dst string, perm os.FileMode) (int64, error)
}

// Publisher stores an event with a chosen id (eventbus.Bus).
type Publisher interface {
	Publish(ctx context.Context, eventType, id string, payload []byte) error
}

// Recorder receives the job metrics (metrics.Metrics).
type Recorder interface {
	UploadJob(result string)
	UploadTranscoded(d time.Duration)
	UploadSpeed(speed float64)
	UploadQueueWait(d time.Duration)
}

// Results of media_upload_jobs_total.
const (
	ResultSucceeded = "succeeded"
	ResultFailed    = "failed"
	// ResultRetryable: failed with upload.failed.v1 {retryable: true}.
	ResultRetryable = "retryable"
	ResultSkipped   = "skipped"
	// ResultRequeued: interrupted by a shutdown, JetStream redelivers it.
	ResultRequeued = "requeued"
)

// Config -.
type Config struct {
	WorkDir     string
	FFmpegPath  string
	FFprobePath string
	Preset      string
	// Bucket is the only bucket source objects are read from.
	Bucket      string
	MaxJobs     int
	MaxLive     int
	SegmentSec  int
	MaxDuration time.Duration
}

// Deps are the collaborators of a Transcoder.
type Deps struct {
	Queue  Queue
	Live   LiveSessions
	Source Source
	Store  storage.ObjectStore
	Pub    Publisher
	Rec    Recorder
	Log    logger.Interface
}

// Transcoder runs at most Config.MaxJobs jobs at once.
type Transcoder struct {
	cfg Config
	Deps

	slots chan struct{}

	mu      sync.Mutex
	running map[string]bool

	// Waits between polls; shortened in tests.
	liveFullWait, fetchBackoff, retryBackoff time.Duration
}

// New -.
func New(cfg Config, d Deps) *Transcoder {
	return &Transcoder{
		cfg:          cfg,
		Deps:         d,
		slots:        make(chan struct{}, max(cfg.MaxJobs, 1)),
		running:      map[string]bool{},
		liveFullWait: _liveFullWait,
		fetchBackoff: _fetchBackoff,
		retryBackoff: _retryBackoff,
	}
}

// Run removes jobs a previous process left half done, then fetches and runs jobs until ctx is canceled.
// It returns after the running jobs stopped (their events are redelivered by JetStream).
func (t *Transcoder) Run(ctx context.Context) {
	root := filepath.Join(t.cfg.WorkDir, _uploadsDir)
	if err := os.RemoveAll(root); err != nil {
		t.Log.Warn("transcode - clean %s: %v", root, err)
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			return
		case t.slots <- struct{}{}:
		}

		m, ok := t.next(ctx)
		if !ok {
			<-t.slots

			continue
		}

		wg.Go(func() {
			defer func() { <-t.slots }()

			t.handle(ctx, m)
		})
	}
}

// next fetches one event while live streams leave room. A job fetched just as a live stream took the last
// slot goes back to the queue for 30 s, except on its last delivery: dropping it would leave the VOD
// PROCESSING forever, and ffmpeg's lower priority still keeps live first.
func (t *Transcoder) next(ctx context.Context) (Message, bool) {
	if !t.liveHasRoom() {
		sleep(ctx, t.liveFullWait)

		return Message{}, false
	}

	m, err := t.Queue.Next(ctx, _fetchWait)
	if err != nil {
		if !errors.Is(err, eventbus.ErrNoEvent) && ctx.Err() == nil {
			t.Log.Warn("transcode - fetch: %v", err)
			sleep(ctx, t.fetchBackoff)
		}

		return Message{}, false
	}

	if !t.liveHasRoom() && m.Delivered < MaxDeliver {
		t.settle(m, func(d Delivery) error { return d.NakWithDelay(_liveFullDelay) })

		return Message{}, false
	}

	return m, true
}

// liveHasRoom is the admission rule: jobs only use capacity live streams leave free.
func (t *Transcoder) liveHasRoom() bool {
	return t.Live.Active() < t.cfg.MaxLive
}

// claim prevents two jobs for the same VOD from sharing a work directory.
func (t *Transcoder) claim(vodID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running[vodID] {
		return false
	}

	t.running[vodID] = true

	return true
}

func (t *Transcoder) unclaim(vodID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.running, vodID)
}

// settle acks, naks or terminates m; a lost acknowledgement only means a redelivery.
func (t *Transcoder) settle(m Message, f func(Delivery) error) {
	if err := f(m.Delivery); err != nil {
		t.Log.Warn("transcode - settle %s: %v", m.Event.ID, err)
	}
}

func (t *Transcoder) jobDir(vodID string) string {
	return filepath.Join(t.cfg.WorkDir, _uploadsDir, vodID)
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// PullerQueue adapts an eventbus.Puller to Queue.
func PullerQueue(p *eventbus.Puller) Queue {
	return pullerQueue{p: p}
}

type pullerQueue struct {
	p *eventbus.Puller
}

func (q pullerQueue) Next(ctx context.Context, maxWait time.Duration) (Message, error) {
	m, err := q.p.Next(ctx, maxWait)
	if err != nil {
		return Message{}, fmt.Errorf("transcode - next: %w", err)
	}

	return Message{Event: m.Event, Delivered: m.Delivered, Seq: m.Seq, Delivery: m}, nil
}
