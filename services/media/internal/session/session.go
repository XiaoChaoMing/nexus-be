// Package session owns the lifecycle of every stream: reserve → transcode → interrupted (grace) → finalized VOD.
package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	ev "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/authz"
	"github.com/minhnn/nexuslive-be/services/media/internal/entity"
	"github.com/minhnn/nexuslive-be/services/media/internal/events"
	"github.com/minhnn/nexuslive-be/services/media/internal/ffmpegcmd"
	"github.com/minhnn/nexuslive-be/services/media/internal/hlsfs"
	"github.com/minhnn/nexuslive-be/services/media/internal/ladder"
	"github.com/minhnn/nexuslive-be/services/media/internal/pipeline"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
	"github.com/minhnn/nexuslive-be/services/media/internal/uploader"
)

const (
	_dirPerm        = 0o750
	_bitsPerKbit    = 1000
	_flushTimeout   = 10 * time.Second
	_publishTimeout = 3 * time.Second
)

// State -.
type State string

// States.
const (
	StateStarting    State = "starting" // publish accepted, waiting for metadata / first frames
	StateLive        State = "live"
	StateInterrupted State = "interrupted"
	StateEnded       State = "ended"
)

// Config -.
type Config struct {
	WorkDir              string
	FFmpegPath           string
	Preset               string
	MaxConcurrentStreams int
	GracePeriod          time.Duration
	// OnReject is called for every refused publish (metrics); optional.
	OnReject func(reason string)
}

// Publish rejection reasons (metrics label, /healthz).
const (
	RejectCapacity      = "capacity"
	RejectInvalidKey    = "invalid_key"
	RejectAlreadyLive   = "already_live"
	RejectRecentlyEnded = "recently_ended"
	RejectError         = "error"
)

// Manager -.
type Manager struct {
	cfg   Config
	l     logger.Interface
	store storage.ObjectStore
	pub   events.Publisher

	mu        sync.Mutex
	byID      map[string]*stream
	byChannel map[string]*stream
	closing   bool
	wg        sync.WaitGroup
	rejected  map[string]int64
}

type stream struct {
	id        string
	channelID string
	dir       string
	createdAt time.Time

	state      State
	started    bool // ffmpeg ran at least once → there is something to keep as VOD
	renditions []entity.Rendition
	hasAudio   bool
	resumes    int

	pub   *Publication
	timer *time.Timer
	// endRequested: stream-svc ended the stream, so the next disconnect finalizes without grace.
	endRequested bool
	// startNumber is the first segment number; a resume stream-svc knows about but this process
	// does not (restart) starts far above any earlier number so no stored object is overwritten.
	startNumber int
	source      entity.Source

	up       *uploader.Uploader
	upCancel context.CancelFunc
	upDone   chan struct{}
}

// Publication is one RTMP publish attached to a stream.
type Publication struct {
	m       *Manager
	s       *stream
	resumed bool
	kick    func()

	mu        sync.Mutex
	pipe      *pipeline.Pipeline
	closed    bool
	startedAt time.Time
	bytesIn   int64
}

// NewManager -.
func NewManager(cfg Config, l logger.Interface, store storage.ObjectStore, pub events.Publisher) *Manager {
	return &Manager{
		cfg:       cfg,
		l:         l,
		store:     store,
		pub:       pub,
		byID:      map[string]*stream{},
		byChannel: map[string]*stream{},
		rejected:  map[string]int64{},
	}
}

// Rejected records a refused publish for /healthz and metrics.
func (m *Manager) Rejected(reason string) {
	m.mu.Lock()
	m.rejected[reason]++
	m.mu.Unlock()

	if m.cfg.OnReject != nil {
		m.cfg.OnReject(reason)
	}
}

// Rejections returns refused publishes by reason since start.
func (m *Manager) Rejections() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[string]int64, len(m.rejected))
	for k, v := range m.rejected {
		out[k] = v
	}

	return out
}

// Begin reserves a transcoding slot for an authorized publish. kick must close the RTMP connection.
func (m *Manager) Begin(g authz.Grant, kick func()) (*Publication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closing {
		return nil, entity.ErrCapacity
	}

	s := m.byChannel[g.ChannelID]
	if s != nil && (s.state == StateLive || s.state == StateStarting) {
		return nil, entity.ErrAlreadyLive
	}

	if m.activeLocked() >= m.cfg.MaxConcurrentStreams {
		return nil, entity.ErrCapacity
	}

	s, resumed, err := m.claimLocked(g, s)
	if err != nil {
		return nil, err
	}

	s.state = StateStarting
	p := &Publication{m: m, s: s, resumed: resumed, kick: kick}
	s.pub = p

	return p, nil
}

// claimLocked resumes the channel's interrupted stream when the grant continues it; otherwise it
// starts a new stream, finishing an interrupted predecessor in the background.
func (m *Manager) claimLocked(g authz.Grant, s *stream) (*stream, bool, error) {
	// Same stream (or L1, where stream-svc does not assign ids). timer.Stop false means the grace
	// period just expired and finalize owns the old stream.
	if s.interrupted() && (g.StreamID == "" || g.StreamID == s.id) && s.timer.Stop() {
		return s, s.started, nil
	}

	if s.interrupted() && s.timer.Stop() {
		// stream-svc started a new stream for this channel: the old one is over, make it a VOD now.
		go func(old *stream) {
			defer m.wg.Done()

			m.finalize(old)
		}(s)
	}

	next, err := m.newStreamLocked(g)
	if err != nil {
		return nil, false, err
	}

	if g.Resumed {
		m.l.Warn("session %s: stream-svc resumes a stream unknown to this process; numbering segments from a new base", next.id)
		next.startNumber = int(time.Now().Unix())
	}

	return next, false, nil
}

// interrupted is false for a nil stream (the channel has none in this process).
func (s *stream) interrupted() bool {
	return s != nil && s.state == StateInterrupted
}

// StreamID -.
func (p *Publication) StreamID() string {
	return p.s.id
}

// Start spawns ffmpeg once the source is known. A resume keeps the previous ladder and continues numbering.
func (p *Publication) Start(src entity.Source) error {
	m, s := p.m, p.s

	m.mu.Lock()

	if p.resumed && src.HasAudio != s.hasAudio {
		ns, err := m.replaceStreamLocked(p)
		if err != nil {
			m.mu.Unlock()

			return err
		}

		s = ns
	}

	opts, err := m.ffmpegOptionsLocked(p, s, src)
	if err != nil {
		m.mu.Unlock()

		return err
	}

	m.mu.Unlock()

	pipe, err := pipeline.Start(m.l, s.id, m.cfg.FFmpegPath, s.dir, opts)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.pipe = pipe
	p.startedAt = time.Now()
	closed := p.closed
	p.mu.Unlock()

	if closed { // connection dropped while we were spawning
		pipe.Stop()

		return pipeline.ErrExited
	}

	m.mu.Lock()
	s.started = true
	s.state = StateLive
	m.mu.Unlock()

	m.l.Info("session %s: live, renditions=%v audio=%t resumed=%t",
		s.id, renditionNames(opts.Renditions), opts.HasAudio, p.resumed)

	m.emit(ev.TypeIngestStarted, ev.IngestStarted{
		StreamID: s.id, ChannelID: s.channelID, Renditions: renditionNames(opts.Renditions),
		SourceWidth: s.source.Width, SourceHeight: s.source.Height, Resumed: p.resumed,
	})

	go p.watch(pipe)

	return nil
}

// replaceStreamLocked handles a reconnect whose audio presence changed: HLS variants cannot change
// shape mid-stream, so the old stream becomes a VOD and the publication moves to a new one.
func (m *Manager) replaceStreamLocked(p *Publication) (*stream, error) {
	old := p.s
	m.l.Warn("session %s: audio presence changed on reconnect, starting a new stream", old.id)

	old.state = StateInterrupted
	old.pub = nil

	ns, err := m.newStreamLocked(authz.Grant{ChannelID: old.channelID})
	if err != nil {
		return nil, err
	}

	ns.state = StateStarting
	ns.pub = p
	p.s = ns
	p.resumed = false

	go func() {
		defer m.wg.Done()

		m.finalize(old)
	}()

	return ns, nil
}

// stopReasonLocked names why the publisher went away, for ingest.stopped.
func (m *Manager) stopReasonLocked(s *stream, reason string) string {
	switch {
	case s.endRequested:
		return ev.ReasonKicked
	case m.closing:
		return ev.ReasonShutdown
	}

	return reason
}

// dropUnstartedAndUnlock removes a stream that never produced output; it releases m.mu.
func (m *Manager) dropUnstartedAndUnlock(s *stream, reason string) {
	s.state = StateEnded
	m.removeLocked(s)
	m.mu.Unlock()

	// stream-svc already created the stream on authorize; without this it would stay LIVE forever.
	// It now goes INTERRUPTED and ends after its grace period (no VOD, nothing was recorded).
	m.emit(ev.TypeIngestStopped, ev.IngestStopped{StreamID: s.id, Reason: reason})

	go func() {
		defer m.wg.Done()

		m.discard(s)
	}()
}

// ffmpegOptionsLocked builds the ffmpeg command options: a resume keeps the previous ladder, appends to the
// playlists and reopens them; a fresh start picks the ladder for the source.
func (m *Manager) ffmpegOptionsLocked(p *Publication, s *stream, src entity.Source) (ffmpegcmd.Options, error) {
	opts := ffmpegcmd.Options{HasAudio: src.HasAudio, Preset: m.cfg.Preset, StartNumber: s.startNumber}

	if p.resumed {
		opts.Renditions = s.renditions
		opts.HasAudio = s.hasAudio
		opts.Append = true

		last, err := hlsfs.LastSegmentNumber(s.dir, renditionNames(s.renditions))
		if err != nil {
			return ffmpegcmd.Options{}, fmt.Errorf("session - resume %s: %w", s.id, err)
		}

		// With append_list ffmpeg numbers new segments after the ones it parsed, and writes start_number
		// as EXT-X-MEDIA-SEQUENCE. Keeping the original start keeps the sequence stable for players and
		// continues file numbering at last+1, so nothing on disk or in storage is overwritten.
		opts.StartNumber = max(s.startNumber, ffmpegcmd.FirstSegmentNumber)
		s.resumes++

		m.l.Info("session %s: resuming after seg_%05d", s.id, last)

		// ffmpeg closed the playlists with ENDLIST when the previous publish stopped; reopen them.
		for _, r := range s.renditions {
			if _, err = hlsfs.StripEndList(filepath.Join(s.dir, r.Name, "index.m3u8")); err != nil {
				m.l.Warn("session %s: reopen playlist %s: %v", s.id, r.Name, err)
			}
		}
	} else {
		s.renditions = ladder.For(src.Height)
		s.hasAudio = src.HasAudio
		s.source = src
		opts.Renditions = s.renditions
	}

	return opts, nil
}

// WriteTag -.
func (p *Publication) WriteTag(tagType byte, timestamp uint32, body []byte) error {
	p.mu.Lock()
	pipe := p.pipe
	p.mu.Unlock()

	if pipe == nil {
		return pipeline.ErrExited
	}

	p.mu.Lock()
	p.bytesIn += int64(len(body))
	p.mu.Unlock()

	return pipe.WriteTag(tagType, timestamp, body)
}

// Close ends this publish (connection gone). The stream waits GracePeriod for a reconnect before becoming VOD.
func (p *Publication) Close(reason string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()

		return
	}

	p.closed = true
	pipe := p.pipe
	p.mu.Unlock()

	m, s := p.m, p.s

	if pipe != nil && !pipe.Stop() {
		m.l.Warn("session %s: ffmpeg exited uncleanly: %v", s.id, pipe.Err())
	}

	m.mu.Lock()

	if s.pub != p {
		m.mu.Unlock()

		return
	}

	s.pub = nil

	if !s.started { // nothing was produced: drop the reservation
		m.dropUnstartedAndUnlock(s, reason)

		return
	}

	s.state = StateInterrupted
	closing := m.closing || s.endRequested

	reason = m.stopReasonLocked(s, reason)

	if !closing {
		s.timer = time.AfterFunc(m.cfg.GracePeriod, func() {
			defer m.wg.Done()

			m.finalize(s)
		})
	}

	m.mu.Unlock()

	m.l.Info("session %s: interrupted (%s), grace %s", s.id, reason, m.cfg.GracePeriod)
	m.emit(ev.TypeIngestStopped, ev.IngestStopped{StreamID: s.id, Reason: reason})

	if closing {
		go func() {
			defer m.wg.Done()

			m.finalize(s)
		}()
	}
}

// watch kicks the publisher when ffmpeg dies on its own; OBS then reconnects and numbering continues.
func (p *Publication) watch(pipe *pipeline.Pipeline) {
	<-pipe.Done()

	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()

	if !closed {
		p.m.l.Error("session %s: ffmpeg exited while live: %v", p.s.id, pipe.Err())
		p.kick()
	}
}

// Shutdown kicks every publisher and finalizes every stream (ENDLIST + flush), waiting until ctx expires.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closing = true

	var kicks []func()

	for _, s := range m.byID {
		switch {
		case s.pub != nil:
			kicks = append(kicks, s.pub.kick)
		case s.state == StateInterrupted && s.timer != nil && s.timer.Stop():
			go func(s *stream) {
				defer m.wg.Done()

				m.finalize(s)
			}(s)
		}
	}
	m.mu.Unlock()

	for _, k := range kicks {
		k()
	}

	done := make(chan struct{})

	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("session - Shutdown: %w", ctx.Err())
	}
}

func (m *Manager) newStreamLocked(g authz.Grant) (*stream, error) {
	id := g.StreamID
	if id == "" {
		id = uuid.NewString()
	}

	// The directory name comes from a server-generated UUID, never from the publish name.
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("session - invalid stream id %q: %w", id, err)
	}

	dir := filepath.Join(m.cfg.WorkDir, id)
	if err := os.MkdirAll(dir, _dirPerm); err != nil {
		return nil, fmt.Errorf("session - mkdir: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &stream{
		id:        id,
		channelID: g.ChannelID,
		dir:       dir,
		createdAt: time.Now(),
		up:        uploader.New(m.store, dir, id),
		upCancel:  cancel,
		upDone:    make(chan struct{}),
	}

	go func() {
		defer close(s.upDone)

		s.up.Run(ctx)
	}()

	m.byID[id] = s
	m.byChannel[g.ChannelID] = s
	m.wg.Add(1) // released by finalize or discard

	return s, nil
}

func (m *Manager) removeLocked(s *stream) {
	delete(m.byID, s.id)

	if m.byChannel[s.channelID] == s {
		delete(m.byChannel, s.channelID)
	}
}

func (m *Manager) activeLocked() int {
	n := 0

	for _, s := range m.byID {
		if s.state == StateLive || s.state == StateStarting {
			n++
		}
	}

	return n
}

func (m *Manager) discard(s *stream) {
	s.upCancel()
	<-s.upDone

	if err := os.RemoveAll(s.dir); err != nil {
		m.l.Warn("session %s: remove dir: %v", s.id, err)
	}
}

// finalize turns an interrupted stream into a VOD: ENDLIST, last upload, cleanup, event.
func (m *Manager) finalize(s *stream) {
	m.mu.Lock()
	if s.state != StateInterrupted {
		m.mu.Unlock()

		return
	}

	s.state = StateEnded
	m.removeLocked(s)
	m.mu.Unlock()

	var duration float64

	for i, r := range s.renditions {
		pl := filepath.Join(s.dir, r.Name, "index.m3u8")

		added, err := hlsfs.EnsureEndList(pl)
		if err != nil {
			m.l.Warn("session %s: ENDLIST %s: %v", s.id, r.Name, err)
		} else if added {
			m.l.Info("session %s: appended missing ENDLIST to %s", s.id, r.Name)
		}

		if i == 0 {
			duration = playlistDuration(pl)
		}
	}

	s.upCancel()
	<-s.upDone

	ctx, cancel := context.WithTimeout(context.Background(), _flushTimeout)
	defer cancel()

	if err := s.up.Flush(ctx); err != nil {
		m.l.Error("session %s: final upload incomplete, keeping %s: %v", s.id, s.dir, err)
	} else if err = os.RemoveAll(s.dir); err != nil {
		m.l.Warn("session %s: remove dir: %v", s.id, err)
	}

	m.l.Info("session %s: ended, VOD %.1fs, resumes=%d", s.id, duration, s.resumes)
	m.emit(ev.TypeVODProcessed, ev.VODProcessed{
		StreamID:      s.id,
		DurationSec:   duration,
		PlaylistPath:  storage.StreamsPrefix + s.id + "/master.m3u8",
		ThumbnailPath: storage.StreamsPrefix + s.id + "/thumb.jpg",
	})
}

func (m *Manager) emit(eventType string, payload any) {
	ctx, cancel := context.WithTimeout(context.Background(), _publishTimeout)
	defer cancel()

	if err := m.pub.Publish(ctx, eventType, payload); err != nil {
		m.l.Error("session: publish %s: %v", eventType, err)
	}
}

// HasCapacity is checked before asking stream-svc, so a publish that cannot be transcoded
// does not create a stream row first.
func (m *Manager) HasCapacity() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return !m.closing && m.activeLocked() < m.cfg.MaxConcurrentStreams
}

// EndStream handles stream.ended: kick the publisher (if any) and finalize the VOD without grace.
func (m *Manager) EndStream(streamID string) {
	m.mu.Lock()

	s := m.byID[streamID]
	if s == nil {
		m.mu.Unlock()

		return
	}

	s.endRequested = true

	if s.pub != nil {
		kick := s.pub.kick
		m.mu.Unlock()

		m.l.Info("session %s: ended by stream-svc, kicking publisher", streamID)
		kick()

		return
	}

	finalizeNow := s.state == StateInterrupted && s.timer != nil && s.timer.Stop()
	m.mu.Unlock()

	if finalizeNow {
		m.l.Info("session %s: ended by stream-svc", streamID)

		go func() {
			defer m.wg.Done()

			m.finalize(s)
		}()
	}
}

// RunHealth publishes ingest.health for every transcoding stream until ctx ends.
func (m *Manager) RunHealth(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, h := range m.health() {
				m.emit(ev.TypeIngestHealth, h)
			}
		}
	}
}

func (m *Manager) health() []ev.IngestHealth {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []ev.IngestHealth

	for _, s := range m.byID {
		if s.state != StateLive || s.pub == nil {
			continue
		}

		p := s.pub
		p.mu.Lock()

		if p.pipe != nil {
			pr := p.pipe.Progress()
			h := ev.IngestHealth{
				StreamID:      s.id,
				FPS:           pr.FPS,
				DroppedFrames: pr.DropFrames,
				Speed:         pr.Speed,
				UploadErrors:  s.up.Errors(),
			}

			if secs := time.Since(p.startedAt).Seconds(); secs > 0 {
				h.BitrateKbps = float64(p.bytesIn) * 8 / _bitsPerKbit / secs
			}

			out = append(out, h)
		}

		p.mu.Unlock()
	}

	return out
}

// Info is a read-only view for /dev/sessions and health.
type Info struct {
	StreamID     string            `json:"streamId"`
	ChannelID    string            `json:"channelId"`
	State        State             `json:"state"`
	CreatedAt    time.Time         `json:"createdAt"`
	Renditions   []string          `json:"renditions"`
	HasAudio     bool              `json:"hasAudio"`
	Resumes      int               `json:"resumes"`
	UploadErrors int64             `json:"uploadErrors"`
	Progress     pipeline.Progress `json:"progress"`
	MasterPath   string            `json:"masterPath"`
}

// List -.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Info, 0, len(m.byID))

	for _, s := range m.byID {
		in := Info{
			StreamID:     s.id,
			ChannelID:    s.channelID,
			State:        s.state,
			CreatedAt:    s.createdAt,
			Renditions:   renditionNames(s.renditions),
			HasAudio:     s.hasAudio,
			Resumes:      s.resumes,
			UploadErrors: s.up.Errors(),
			MasterPath:   storage.StreamsPrefix + s.id + "/master.m3u8",
		}

		if s.pub != nil {
			s.pub.mu.Lock()
			if s.pub.pipe != nil {
				in.Progress = s.pub.pipe.Progress()
			}
			s.pub.mu.Unlock()
		}

		out = append(out, in)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })

	return out
}

// Active -.
func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.activeLocked()
}

func renditionNames(rs []entity.Rendition) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}

	return out
}

func playlistDuration(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	d, err := hlsfs.Duration(data)
	if err != nil {
		return 0
	}

	return d
}
