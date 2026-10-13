package session_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ev "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/session"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
	"github.com/stretchr/testify/require"
)

type recPublisher struct {
	mu     sync.Mutex
	events []string
	vods   []ev.VODProcessed
}

func (p *recPublisher) Publish(_ context.Context, eventType string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.events = append(p.events, eventType)

	if v, ok := payload.(ev.VODProcessed); ok {
		p.vods = append(p.vods, v)
	}

	return nil
}

func write(t *testing.T, path, body string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

const (
	_crashed = "0199d4a2-0000-7000-8000-000000000001"
	_empty   = "0199d4a2-0000-7000-8000-000000000002"
	// Two segments, no ENDLIST: what ffmpeg leaves when the process is killed mid-stream.
	_eventPlaylist = "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:2.000000,\nseg_00001.ts\n#EXTINF:2.000000,\nseg_00002.ts\n"
)

func crashedStream(t *testing.T, workDir, id string) string {
	t.Helper()

	dir := filepath.Join(workDir, id)
	write(t, filepath.Join(dir, "master.m3u8"), "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n720p/index.m3u8\n")
	write(t, filepath.Join(dir, "720p", "index.m3u8"), _eventPlaylist)
	write(t, filepath.Join(dir, "720p", "seg_00001.ts"), "a")
	write(t, filepath.Join(dir, "720p", "seg_00002.ts"), "b")

	return dir
}

func newManager(workDir string, store storage.ObjectStore, pub *recPublisher) *session.Manager {
	return session.NewManager(session.Config{WorkDir: workDir, MaxConcurrentStreams: 1, GracePeriod: time.Second},
		logger.New("error"), store, pub)
}

func TestRecoverClosesCrashedStreams(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	crashedDir := crashedStream(t, work, _crashed)
	write(t, filepath.Join(work, _empty, "720p", "seg_00001.ts.tmp"), "partial") // crashed while starting
	write(t, filepath.Join(work, "not-a-stream", "x"), "kept")                   // not ours: untouched

	store, pub := storage.NewMemory(), &recPublisher{}

	res, err := newManager(work, store, pub).Recover(context.Background())
	require.NoError(t, err)
	require.Equal(t, session.RecoverResult{Recovered: 1, Discarded: 1}, res)

	pl, ok := store.Get("streams/" + _crashed + "/720p/index.m3u8")
	require.True(t, ok)
	require.Contains(t, string(pl.Body), "#EXT-X-ENDLIST", "the VOD playlist is closed")

	_, ok = store.Get("streams/" + _crashed + "/720p/seg_00002.ts")
	require.True(t, ok, "segments the crashed process had not uploaded are uploaded now")

	require.Equal(t, []string{ev.TypeIngestStopped, ev.TypeVODProcessed}, pub.events)
	require.Equal(t, _crashed, pub.vods[0].StreamID)
	require.InDelta(t, 4.0, pub.vods[0].DurationSec, 0.01)

	require.NoDirExists(t, crashedDir)
	require.NoDirExists(t, filepath.Join(work, _empty))
	require.DirExists(t, filepath.Join(work, "not-a-stream"))
}

func TestRecoverKeepsDirWhenUploadFails(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	dir := crashedStream(t, work, _crashed)

	store, pub := storage.NewMemory(), &recPublisher{}
	store.FailNext = 1 << 20

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	res, err := newManager(work, store, pub).Recover(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Kept)
	require.DirExists(t, dir, "retried on the next start")
	require.Empty(t, pub.events, "stream-svc is not told the stream ended without a VOD")
}

func TestRejectionsCounted(t *testing.T) {
	t.Parallel()

	var got []string

	m := session.NewManager(session.Config{WorkDir: t.TempDir(), MaxConcurrentStreams: 1, OnReject: func(r string) { got = append(got, r) }},
		logger.New("error"), storage.NewMemory(), &recPublisher{})

	m.Rejected(session.RejectCapacity)
	m.Rejected(session.RejectCapacity)
	m.Rejected(session.RejectRecentlyEnded)

	require.Equal(t, map[string]int64{session.RejectCapacity: 2, session.RejectRecentlyEnded: 1}, m.Rejections())
	require.Equal(t, []string{session.RejectCapacity, session.RejectCapacity, session.RejectRecentlyEnded}, got)
}
