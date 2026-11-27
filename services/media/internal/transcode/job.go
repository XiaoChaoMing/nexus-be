package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	ev "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/media/internal/entity"
	"github.com/minhnn/nexuslive-be/services/media/internal/ffmpegcmd"
	"github.com/minhnn/nexuslive-be/services/media/internal/ladder"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
	"github.com/minhnn/nexuslive-be/services/media/internal/uploader"
)

const (
	// Transient failures are tried again this many times before upload.failed.v1 {retryable: true}.
	_transientRetries = 2
	_retryBackoff     = 10 * time.Second
	_downloadTimeout  = 30 * time.Minute
	// Time limit of a job: 3 × the video's duration + 10 min.
	_limitFactor = 3
	_limitExtra  = 10 * time.Minute
	// The thumbnail frame is taken at 10% of the duration.
	_thumbFraction = 0.1
	_publishTries  = 3
	_publishWait   = 2 * time.Second
	_publishTimout = 5 * time.Second
	_sourceName    = "source"
	_outName       = "out"
	_masterName    = "master.m3u8"
	_thumbName     = "thumb.jpg"
)

// _objectKeyRe is the only source key stream-svc writes: sources/{uploadId}.
var _objectKeyRe = regexp.MustCompile(`^sources/([0-9a-f-]{36})$`)

// result is a transcoded and stored video.
type result struct {
	probe      Probe
	renditions []entity.Rendition
	thumb      bool
}

// handle runs one job to its end: upload.processed.v1 or upload.failed.v1, then ack. An event that can never
// be read is terminated; a job interrupted by a shutdown goes back to the queue.
func (t *Transcoder) handle(ctx context.Context, m Message) {
	p, err := t.decode(m.Event.Payload)
	if errors.Is(err, eventbus.ErrSkip) {
		t.Log.Warn("transcode - event %s: %v", m.Event.ID, err)
		t.Rec.UploadJob(ResultSkipped)
		t.settle(m, Delivery.Term)

		return
	}

	if !t.claim(p.VODID) {
		t.settle(m, func(d Delivery) error { return d.NakWithDelay(_liveFullDelay) })

		return
	}
	defer t.unclaim(p.VODID)

	if !m.Event.OccurredAt.IsZero() {
		t.Rec.UploadQueueWait(time.Since(m.Event.OccurredAt))
	}

	var res result

	if err == nil {
		prog := newProgress()
		stop := t.keepAlive(ctx, p.VODID, m.Delivery, prog)
		res, err = t.process(ctx, p, prog)

		stop()
	}

	t.finish(ctx, m, p.VODID, res, err)
}

// decode returns ErrSkip for an event without a usable vodId (no VOD to fail) and ErrBadSource, with the
// payload, for a source media-svc must not read.
func (t *Transcoder) decode(payload []byte) (ev.UploadCompleted, error) {
	var p ev.UploadCompleted
	if err := json.Unmarshal(payload, &p); err != nil {
		return p, fmt.Errorf("%w: %w", eventbus.ErrSkip, err)
	}

	if _, err := uuid.Parse(p.VODID); err != nil || len(p.VODID) != len(uuid.Nil.String()) {
		return p, fmt.Errorf("%w: bad vodId", eventbus.ErrSkip)
	}

	if p.Bucket != t.cfg.Bucket {
		return p, fmt.Errorf("%w: bucket %q", ErrBadSource, p.Bucket)
	}

	k := _objectKeyRe.FindStringSubmatch(p.ObjectKey)
	if k == nil {
		return p, fmt.Errorf("%w: key %q", ErrBadSource, p.ObjectKey)
	}

	if _, err := uuid.Parse(k[1]); err != nil {
		return p, fmt.Errorf("%w: key %q", ErrBadSource, p.ObjectKey)
	}

	return p, nil
}

// finish publishes the outcome and acks. When the outcome cannot be published the event is redelivered,
// so the VOD never stays PROCESSING because of a lost publish.
func (t *Transcoder) finish(ctx context.Context, m Message, vodID string, res result, err error) {
	interrupted := err != nil && ctx.Err() != nil
	if interrupted && m.Delivered < MaxDeliver {
		t.Log.Info("transcode - %s interrupted by shutdown, requeued", vodID)
		t.Rec.UploadJob(ResultRequeued)
		t.settle(m, func(d Delivery) error { return d.NakWithDelay(_shutdownDelay) })

		return
	}

	if interrupted {
		err = fmt.Errorf("%w: interrupted on the last delivery: %w", ErrTimeLimit, err)
	}

	eventType, id, payload, outcome := t.outcome(m, vodID, res, err)

	if perr := t.publishFixed(context.WithoutCancel(ctx), eventType, id, payload); perr != nil {
		t.Log.Error("transcode - %s: publish %s: %v", vodID, eventType, perr)
		t.settle(m, func(d Delivery) error { return d.NakWithDelay(_liveFullDelay) })

		return
	}

	t.Rec.UploadJob(outcome)
	t.settle(m, Delivery.Ack)
}

func (t *Transcoder) outcome(m Message, vodID string, res result, err error) (eventType, id string, payload any, jobResult string) {
	if err == nil {
		t.Log.Info("transcode - %s ready: %.0f s, %d renditions", vodID, res.probe.DurationSec, len(res.renditions))

		return ev.TypeUploadProcessed, "upload-processed-" + vodID, processedPayload(vodID, res), ResultSucceeded
	}

	f := Classify(err)
	t.Log.Warn("transcode - %s failed (%s, retryable=%t): %v", vodID, f.Reason, f.Retryable, err)

	jobResult = ResultFailed
	if f.Retryable {
		jobResult = ResultRetryable
	}

	// The stream sequence differs for every upload.completed.v1, so a creator's retry is never deduplicated
	// against the failure of the previous attempt.
	id = "upload-failed-" + vodID + "-" + strconv.FormatUint(m.Seq, 10)

	return ev.TypeUploadFailed, id, ev.UploadFailed{VODID: vodID, Reason: f.Reason, Retryable: f.Retryable}, jobResult
}

func processedPayload(vodID string, res result) ev.UploadProcessed {
	prefix := storage.VideosPrefix + vodID + "/"
	names := make([]string, len(res.renditions))

	for i, r := range res.renditions {
		names[i] = r.Name
	}

	thumb := ""
	if res.thumb {
		thumb = prefix + _thumbName
	}

	return ev.UploadProcessed{
		VODID:         vodID,
		DurationSec:   res.probe.DurationSec,
		PlaylistPath:  prefix + _masterName,
		ThumbnailPath: thumb,
		Renditions:    names,
		SourceWidth:   res.probe.Width,
		SourceHeight:  res.probe.Height,
	}
}

// process runs the job, trying transient failures again.
func (t *Transcoder) process(ctx context.Context, p ev.UploadCompleted, prog *progress) (result, error) {
	for attempt := 0; ; attempt++ {
		res, err := t.attempt(ctx, p, prog)
		if err == nil || !transient(err) || attempt >= _transientRetries || ctx.Err() != nil {
			return res, err
		}

		t.Log.Warn("transcode - %s attempt %d: %v", p.VODID, attempt+1, err)
		sleep(ctx, t.retryBackoff*time.Duration(attempt+1))
	}
}

// attempt downloads, probes, transcodes and uploads once, in a work directory removed on every path.
func (t *Transcoder) attempt(ctx context.Context, p ev.UploadCompleted, prog *progress) (result, error) {
	dir := t.jobDir(p.VODID)
	out := filepath.Join(dir, _outName)

	defer t.removeDir(dir)

	if err := t.prepareDir(dir, out); err != nil {
		return result{}, err
	}

	prog.set(ev.UploadStageProbing, 0)

	src := filepath.Join(dir, _sourceName)
	if err := t.download(ctx, p, src); err != nil {
		return result{}, err
	}

	probe, err := runProbe(ctx, t.cfg.FFprobePath, src)
	if err != nil {
		return result{}, err
	}

	if verr := probe.Validate(t.cfg.MaxDuration); verr != nil {
		return result{}, verr
	}

	limit := time.Duration(probe.DurationSec*float64(time.Second))*_limitFactor + _limitExtra

	jobCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	res := result{probe: probe, renditions: ladder.For(probe.ShortSide())}
	if eerr := t.encode(jobCtx, ctx, out, res, prog); eerr != nil {
		return result{}, eerr
	}

	prog.set(ev.UploadStageUploading, percentUploading)

	if err = uploader.NewVOD(t.Store, out, storage.VideosPrefix+p.VODID+"/").Flush(jobCtx); err != nil {
		return result{}, t.limitErr(jobCtx, ctx, fmt.Errorf("%w: upload: %w", ErrStorage, err))
	}

	_, statErr := os.Stat(filepath.Join(out, _thumbName))
	res.thumb = statErr == nil

	return res, nil
}

// encode runs ffmpeg for res.
func (t *Transcoder) encode(jobCtx, ctx context.Context, out string, res result, prog *progress) error {
	prog.set(ev.UploadStageTranscoding, percentTranscodeStart)

	args := ffmpegcmd.BuildVOD(ffmpegcmd.VODOptions{
		Input:       filepath.Join("..", _sourceName),
		VideoStream: res.probe.VideoStream,
		Renditions:  res.renditions,
		HasAudio:    res.probe.HasAudio,
		Portrait:    res.probe.Portrait(),
		FPS:         res.probe.FPS,
		SegmentSec:  t.cfg.SegmentSec,
		Preset:      t.cfg.Preset,
		ThumbAtSec:  res.probe.DurationSec * _thumbFraction,
	})

	start := time.Now()
	err := t.runFFmpeg(jobCtx, out, args, func(outSec, speed float64) {
		prog.set(ev.UploadStageTranscoding, transcodePercent(outSec, res.probe.DurationSec))
		t.Rec.UploadSpeed(speed)
	})

	t.Rec.UploadSpeed(0)

	if err != nil {
		return t.limitErr(jobCtx, ctx, err)
	}

	t.Rec.UploadTranscoded(time.Since(start))

	return nil
}

// limitErr tells the job's own time limit (a failure) from a shutdown (the parent context, requeued).
func (t *Transcoder) limitErr(jobCtx, ctx context.Context, err error) error {
	if ctx.Err() == nil && errors.Is(jobCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrTimeLimit, err)
	}

	return err
}

func (t *Transcoder) download(ctx context.Context, p ev.UploadCompleted, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, _downloadTimeout)
	defer cancel()

	n, err := t.Source.Download(ctx, p.Bucket, p.ObjectKey, dst, _filePerm)
	if errors.Is(err, storage.ErrObjectNotFound) {
		return fmt.Errorf("%w: %w", ErrSourceMissing, err)
	}

	if err != nil {
		return fmt.Errorf("%w: download: %w", ErrStorage, err)
	}

	t.Log.Debug("transcode - %s: %d bytes downloaded", p.VODID, n)

	return nil
}

func (t *Transcoder) prepareDir(dir, out string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("transcode - clean %s: %w", dir, err)
	}

	if err := os.MkdirAll(out, _dirPerm); err != nil {
		return fmt.Errorf("transcode - mkdir %s: %w", out, err)
	}

	return nil
}

func (t *Transcoder) removeDir(dir string) {
	if err := os.RemoveAll(dir); err != nil {
		t.Log.Warn("transcode - remove %s: %v", dir, err)
	}
}

// publishFixed publishes with a fixed Nats-Msg-Id (JetStream drops a duplicate inside its window).
func (t *Transcoder) publishFixed(ctx context.Context, eventType, id string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("transcode - marshal %s: %w", eventType, err)
	}

	for try := 1; ; try++ {
		pctx, cancel := context.WithTimeout(ctx, _publishTimout)
		err = t.Pub.Publish(pctx, eventType, id, body)

		cancel()

		if err == nil || try >= _publishTries {
			return err
		}

		sleep(ctx, _publishWait)
	}
}
