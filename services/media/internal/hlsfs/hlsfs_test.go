package hlsfs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhnn/nexuslive-be/services/media/internal/hlsfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _playlist = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:1
#EXT-X-PLAYLIST-TYPE:EVENT
#EXTINF:2.000000,
seg_00001.ts
#EXTINF:2.000000,
seg_00002.ts
#EXT-X-DISCONTINUITY
#EXTINF:2.000000,
seg_00007.ts
`

func TestSegmentsAndEndList(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"seg_00001.ts", "seg_00002.ts", "seg_00007.ts"}, hlsfs.Segments([]byte(_playlist)))
	assert.False(t, hlsfs.HasEndList([]byte(_playlist)))
}

func TestLastSegmentNumber(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "720p"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "720p", "index.m3u8"), []byte(_playlist), 0o600))

	n, err := hlsfs.LastSegmentNumber(dir, []string{"720p", "480p"})
	require.NoError(t, err)
	assert.Equal(t, 7, n)

	n, err = hlsfs.LastSegmentNumber(t.TempDir(), []string{"720p"})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestEnsureEndList(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "index.m3u8")
	require.NoError(t, os.WriteFile(p, []byte(_playlist), 0o600))

	changed, err := hlsfs.EnsureEndList(p)
	require.NoError(t, err)
	assert.True(t, changed)

	changed, err = hlsfs.EnsureEndList(p)
	require.NoError(t, err)
	assert.False(t, changed, "idempotent")

	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.True(t, hlsfs.HasEndList(data))
}
