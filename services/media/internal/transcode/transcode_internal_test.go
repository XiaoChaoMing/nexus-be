package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	ev "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errMinIO = errors.New("minio: connection refused")

type fakeLive struct{ n atomic.Int32 }

func (f *fakeLive) Active() int { return int(f.n.Load()) }

type fakeDelivery struct {
	mu      sync.Mutex
	actions []string
	delay   time.Duration
}

func (d *fakeDelivery) record(a string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.actions = append(d.actions, a)

	return nil
}

func (d *fakeDelivery) Ack() error        { return d.record("ack") }
func (d *fakeDelivery) Term() error       { return d.record("term") }
func (d *fakeDelivery) InProgress() error { return d.record("progress") }

func (d *fakeDelivery) NakWithDelay(delay time.Duration) error {
	d.delay = delay

	return d.record("nak")
}

func (d *fakeDelivery) got() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]string(nil), d.actions...)
}

// fakeQueue returns msg once; afterLive changes the live count when the fetch happens (a race with RTMP).
type fakeQueue struct {
	calls     atomic.Int32
	msg       *Message
	live      *fakeLive
	afterLive int32
}

func (q *fakeQueue) Next(context.Context, time.Duration) (Message, error) {
	q.calls.Add(1)

	if q.msg == nil {
		return Message{}, eventbus.ErrNoEvent
	}

	m := *q.msg
	q.msg = nil

	if q.live != nil {
		q.live.n.Store(q.afterLive)
	}

	return m, nil
}

type fakeSource struct {
	calls atomic.Int32
	err   error
}

func (s *fakeSource) Download(context.Context, string, string, string, os.FileMode) (int64, error) {
	s.calls.Add(1)

	return 0, s.err
}

type published struct {
	eventType, id string
	payload       []byte
}

type fakePub struct {
	mu  sync.Mutex
	out []published
}

func (p *fakePub) Publish(_ context.Context, eventType, id string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.out = append(p.out, published{eventType: eventType, id: id, payload: payload})

	return nil
}

type fakeRec struct {
	mu      sync.Mutex
	results []string
}

func (r *fakeRec) UploadJob(result string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.results = append(r.results, result)
}

func (r *fakeRec) UploadTranscoded(time.Duration) {}
func (r *fakeRec) UploadSpeed(float64)            {}
func (r *fakeRec) UploadQueueWait(time.Duration)  {}

func newTestTranscoder(t *testing.T, d Deps) *Transcoder {
	t.Helper()

	if d.Live == nil {
		d.Live = &fakeLive{}
	}

	if d.Queue == nil {
		d.Queue = &fakeQueue{}
	}

	if d.Source == nil {
		d.Source = &fakeSource{}
	}

	if d.Pub == nil {
		d.Pub = &fakePub{}
	}

	if d.Rec == nil {
		d.Rec = &fakeRec{}
	}

	d.Store = storage.NewMemory()
	d.Log = logger.New("error")

	tr := New(Config{WorkDir: t.TempDir(), Bucket: "nxl-uploads", MaxJobs: 1, MaxLive: 2, SegmentSec: 4}, d)
	tr.liveFullWait, tr.fetchBackoff, tr.retryBackoff = time.Millisecond, time.Millisecond, time.Millisecond

	return tr
}

func TestNext_Admission(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		live      int32
		afterLive int32
		delivered uint64
		fetched   bool
		started   bool
		actions   []string
	}{
		{name: "live leaves room: job starts", live: 1, afterLive: 1, delivered: 1, fetched: true, started: true},
		{name: "live full: nothing fetched", live: 2, afterLive: 2, delivered: 1},
		{name: "live filled during the fetch: back to the queue", live: 1, afterLive: 2, delivered: 1, fetched: true, actions: []string{"nak"}},
		{name: "live filled on the last delivery: runs anyway", live: 1, afterLive: 2, delivered: MaxDeliver, fetched: true, started: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			live := &fakeLive{}
			live.n.Store(tc.live)

			d := &fakeDelivery{}
			q := &fakeQueue{msg: &Message{Delivered: tc.delivered, Delivery: d}, live: live, afterLive: tc.afterLive}
			tr := newTestTranscoder(t, Deps{Live: live, Queue: q})

			_, started := tr.next(t.Context())

			assert.Equal(t, tc.started, started)
			assert.Equal(t, tc.fetched, q.calls.Load() == 1)
			assert.Equal(t, tc.actions, d.got())

			if tc.actions != nil {
				assert.Equal(t, _liveFullDelay, d.delay)
			}
		})
	}
}

func validPayload(t *testing.T) []byte {
	t.Helper()

	return completed(t, nil)
}

func completed(t *testing.T, mutate func(*ev.UploadCompleted)) []byte {
	t.Helper()

	p := ev.UploadCompleted{
		UploadID: uuid.NewString(), VODID: uuid.NewString(), ChannelID: uuid.NewString(),
		Bucket: "nxl-uploads", SizeBytes: 1,
	}
	p.ObjectKey = "sources/" + p.UploadID

	if mutate != nil {
		mutate(&p)
	}

	b, err := json.Marshal(p)
	require.NoError(t, err)

	return b
}

func TestHandle_Failures(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	tests := []struct {
		name      string
		payload   func(t *testing.T) []byte
		sourceErr error
		downloads int32
		actions   []string
		reason    string
		retryable bool
		result    string
	}{
		{
			name:    "not json: terminated",
			payload: func(*testing.T) []byte { return []byte(`{`) },
			actions: []string{"term"}, result: ResultSkipped,
		},
		{
			name: "bad vodId: terminated",
			payload: func(t *testing.T) []byte {
				t.Helper()

				return completed(t, func(p *ev.UploadCompleted) { p.VODID = "../x" })
			},
			actions: []string{"term"}, result: ResultSkipped,
		},
		{
			name: "other bucket: failed, not read",
			payload: func(t *testing.T) []byte {
				t.Helper()

				return completed(t, func(p *ev.UploadCompleted) { p.Bucket = "nxl-media" })
			},
			actions: []string{"ack"}, reason: ev.UploadFailStorageError, result: ResultFailed,
		},
		{
			name: "key outside sources/: failed, not read",
			payload: func(t *testing.T) []byte {
				t.Helper()

				return completed(t, func(p *ev.UploadCompleted) { p.ObjectKey = "sources/../streams/x" })
			},
			actions: []string{"ack"}, reason: ev.UploadFailStorageError, result: ResultFailed,
		},
		{
			name:      "source gone: failed, not retryable",
			payload:   validPayload,
			sourceErr: storage.ErrObjectNotFound, downloads: 1,
			actions: []string{"ack"}, reason: ev.UploadFailStorageError, result: ResultFailed,
		},
		{
			name:      "storage down: two retries, then retryable failure",
			payload:   validPayload,
			sourceErr: errMinIO, downloads: 1 + _transientRetries,
			actions: []string{"ack"}, reason: ev.UploadFailStorageError, retryable: true, result: ResultRetryable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src, pub, rec, d := &fakeSource{err: tc.sourceErr}, &fakePub{}, &fakeRec{}, &fakeDelivery{}
			tr := newTestTranscoder(t, Deps{Source: src, Pub: pub, Rec: rec})

			payload := tc.payload(t)
			tr.handle(t.Context(), Message{Event: eventbus.Event{Payload: payload}, Delivered: 1, Seq: 42, Delivery: d})

			assert.Equal(t, tc.actions, d.got())
			assert.Equal(t, tc.downloads, src.calls.Load())
			assert.Equal(t, []string{tc.result}, rec.results)

			if tc.reason == "" {
				assert.Empty(t, pub.out)

				return
			}

			var in ev.UploadCompleted

			require.NoError(t, json.Unmarshal(payload, &in))
			require.Len(t, pub.out, 1)
			assert.Equal(t, ev.TypeUploadFailed, pub.out[0].eventType)
			assert.Equal(t, "upload-failed-"+in.VODID+"-42", pub.out[0].id)

			var out ev.UploadFailed

			require.NoError(t, json.Unmarshal(pub.out[0].payload, &out))
			assert.Equal(t, ev.UploadFailed{VODID: in.VODID, Reason: tc.reason, Retryable: tc.retryable}, out)
		})
	}
}

func TestHandle_ShutdownRequeues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		delivered uint64
		actions   []string
		published int
	}{
		{name: "redelivered after the restart", delivered: 1, actions: []string{"nak"}},
		{name: "last delivery: retryable failure instead of a stuck VOD", delivered: MaxDeliver, actions: []string{"ack"}, published: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pub, d := &fakePub{}, &fakeDelivery{}
			tr := newTestTranscoder(t, Deps{Source: &fakeSource{err: errMinIO}, Pub: pub})

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			tr.handle(ctx, Message{Event: eventbus.Event{Payload: completed(t, nil)}, Delivered: tc.delivered, Delivery: d})

			assert.Equal(t, tc.actions, d.got())
			assert.Len(t, pub.out, tc.published)
		})
	}
}

func TestClaim(t *testing.T) {
	t.Parallel()

	tr := newTestTranscoder(t, Deps{})

	require.True(t, tr.claim("a"))
	require.False(t, tr.claim("a"), "a second job for the same VOD waits")
	require.True(t, tr.claim("b"))

	tr.unclaim("a")
	require.True(t, tr.claim("a"))
}

func TestTranscodePercent(t *testing.T) {
	t.Parallel()

	assert.Equal(t, percentTranscodeStart, transcodePercent(0, 100))
	assert.Equal(t, percentTranscodeStart, transcodePercent(10, 0))
	assert.Equal(t, 47, transcodePercent(50, 100))
	assert.Equal(t, percentUploading, transcodePercent(150, 100))
}

func TestProgressChanged(t *testing.T) {
	t.Parallel()

	p := newProgress()

	_, ok := p.changed("v")
	require.False(t, ok, "nothing before the first stage")

	p.set(ev.UploadStageTranscoding, 40)

	got, ok := p.changed("v")
	require.True(t, ok)
	assert.Equal(t, ev.UploadProgress{VODID: "v", Percent: 40, Stage: ev.UploadStageTranscoding}, got)

	p.set(ev.UploadStageTranscoding, 30)

	_, ok = p.changed("v")
	require.False(t, ok, "percent never goes back inside a stage")
}
