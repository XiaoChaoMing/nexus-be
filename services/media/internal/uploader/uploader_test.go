package uploader_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
	"github.com/minhnn/nexuslive-be/services/media/internal/uploader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _sid = "11111111-1111-1111-1111-111111111111"

func write(t *testing.T, dir, rel, body string) {
	t.Helper()

	p := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
}

const _pl2 = "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:2.0,\nseg_00000.ts\n#EXTINF:2.0,\nseg_00001.ts\n"

func TestPlaylistWaitsForSegments(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := storage.NewMemory()
	u := uploader.New(store, dir, _sid)
	ctx := context.Background()
	prefix := "streams/" + _sid + "/"

	write(t, dir, "720p/seg_00000.ts", "a")
	write(t, dir, "720p/seg_00001.ts.tmp", "partial") // ffmpeg temp_file: must be ignored
	write(t, dir, "720p/index.m3u8", _pl2)

	require.NoError(t, u.Sync(ctx, false))

	_, ok := store.Get(prefix + "720p/index.m3u8")
	assert.False(t, ok, "playlist must wait until seg_00001 is uploaded")
	_, ok = store.Get(prefix + "720p/seg_00001.ts.tmp")
	assert.False(t, ok)

	require.NoError(t, os.Rename(filepath.Join(dir, "720p", "seg_00001.ts.tmp"), filepath.Join(dir, "720p", "seg_00001.ts")))
	require.NoError(t, u.Sync(ctx, false))

	pl, ok := store.Get(prefix + "720p/index.m3u8")
	require.True(t, ok)
	assert.Equal(t, "no-cache, max-age=1", pl.CacheControl)
	assert.Equal(t, "application/vnd.apple.mpegurl", pl.ContentType)

	seg, _ := store.Get(prefix + "720p/seg_00001.ts")
	assert.Equal(t, "public, max-age=31536000, immutable", seg.CacheControl)

	order := store.Order()
	assert.Less(t, slices.Index(order, prefix+"720p/seg_00001.ts"), slices.Index(order, prefix+"720p/index.m3u8"))

	// Unchanged playlist is not re-uploaded.
	n := len(store.Order())

	require.NoError(t, u.Sync(ctx, false))
	assert.Len(t, store.Order(), n)
}

func TestEndListHiddenUntilFinal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := storage.NewMemory()
	u := uploader.New(store, dir, _sid)
	prefix := "streams/" + _sid + "/"

	write(t, dir, "360p/seg_00000.ts", "a")
	write(t, dir, "360p/seg_00001.ts", "b")
	write(t, dir, "360p/index.m3u8", _pl2+"#EXT-X-ENDLIST\n")
	write(t, dir, "master.m3u8", "#EXTM3U\n")

	require.NoError(t, u.Sync(context.Background(), false))

	pl, _ := store.Get(prefix + "360p/index.m3u8")
	assert.NotContains(t, string(pl.Body), "ENDLIST", "interrupted stream must still look live")

	require.NoError(t, u.Flush(context.Background()))

	pl, _ = store.Get(prefix + "360p/index.m3u8")
	assert.Contains(t, string(pl.Body), "#EXT-X-ENDLIST")
	assert.Equal(t, "public, max-age=3600", pl.CacheControl)

	m, _ := store.Get(prefix + "master.m3u8")
	assert.Equal(t, "public, max-age=3600", m.CacheControl)
}

func TestRetry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := storage.NewMemory()
	store.FailNext = 2
	u := uploader.New(store, dir, _sid)

	write(t, dir, "480p/seg_00000.ts", "a")
	require.NoError(t, u.Sync(context.Background(), false))

	_, ok := store.Get("streams/" + _sid + "/480p/seg_00000.ts")
	assert.True(t, ok)
	assert.Zero(t, u.Errors())
}
