package transcode

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	ev "github.com/minhnn/nexuslive-be/pkg/events"
)

// Overall percent of a job: PROBING (download + ffprobe) 0, TRANSCODING 5–90 following ffmpeg's output
// time, UPLOADING 90. upload.processed.v1 means 100.
const (
	percentTranscodeStart = 5
	percentUploading      = 90
	_transcodeSpan        = percentUploading - percentTranscodeStart

	_progressEvery   = 5 * time.Second
	_inProgressEvery = 30 * time.Second
	_progressTimeout = 2 * time.Second
)

// progress is the job's current stage and percent, read by the reporter goroutine.
type progress struct {
	mu        sync.Mutex
	stage     string
	percent   int
	published ev.UploadProgress
}

func newProgress() *progress {
	return &progress{}
}

// set never moves the percent back inside a stage (stream-svc ignores decreases anyway).
func (p *progress) set(stage string, percent int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if stage == p.stage && percent < p.percent {
		return
	}

	p.stage, p.percent = stage, percent
}

// changed returns the value to publish, or false when nothing changed since the last call.
func (p *progress) changed(vodID string) (ev.UploadProgress, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cur := ev.UploadProgress{VODID: vodID, Percent: p.percent, Stage: p.stage}
	if cur.Stage == "" || cur == p.published {
		return cur, false
	}

	p.published = cur

	return cur, true
}

// transcodePercent maps ffmpeg's output time onto the TRANSCODING span.
func transcodePercent(outSec, durationSec float64) int {
	if durationSec <= 0 || outSec <= 0 {
		return percentTranscodeStart
	}

	done := math.Min(outSec/durationSec, 1)

	return percentTranscodeStart + int(done*_transcodeSpan)
}

// keepAlive publishes upload.progress.v1 every 5 s (best effort) and tells JetStream every 30 s that the
// event is still being worked on. The returned stop waits for the goroutine to end.
func (t *Transcoder) keepAlive(ctx context.Context, vodID string, d Delivery, prog *progress) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)

		report := time.NewTicker(_progressEvery)
		defer report.Stop()

		alive := time.NewTicker(_inProgressEvery)
		defer alive.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-report.C:
				t.publishProgress(ctx, vodID, prog)
			case <-alive.C:
				if err := d.InProgress(); err != nil {
					t.Log.Warn("transcode - %s in progress: %v", vodID, err)
				}
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func (t *Transcoder) publishProgress(ctx context.Context, vodID string, prog *progress) {
	p, ok := prog.changed(vodID)
	if !ok {
		return
	}

	body, err := json.Marshal(p)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, _progressTimeout)
	defer cancel()

	if err = t.Pub.Publish(ctx, ev.TypeUploadProgress, uuid.NewString(), body); err != nil {
		t.Log.Debug("transcode - %s progress: %v", vodID, err)
	}
}
