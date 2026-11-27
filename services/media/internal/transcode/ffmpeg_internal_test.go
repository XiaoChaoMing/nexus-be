package transcode

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	ev "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileSource "downloads" a local file.
type fileSource struct{ path string }

func (s fileSource) Download(_ context.Context, _, _, dst string, perm os.FileMode) (int64, error) {
	in, err := os.Open(s.path)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	return io.Copy(out, in)
}

// TestHandle_FFmpeg transcodes generated clips end to end; it needs ffmpeg and ffprobe with libx264
// (NXL_TEST_FFMPEG=1).
func TestHandle_FFmpeg(t *testing.T) { //nolint:funlen // one end-to-end story
	t.Parallel()

	if os.Getenv("NXL_TEST_FFMPEG") == "" {
		t.Skip("NXL_TEST_FFMPEG not set")
	}

	tests := []struct {
		name       string
		lavfi      []string
		renditions []string
		height     string
	}{
		{
			name:       "1080p60 with audio",
			lavfi:      []string{"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=60", "-f", "lavfi", "-i", "sine=frequency=440"},
			renditions: []string{"720p", "480p", "360p"},
			height:     "RESOLUTION=1280x720",
		},
		{
			name:       "vertical without audio",
			lavfi:      []string{"-f", "lavfi", "-i", "testsrc=size=1080x1920:rate=25"},
			renditions: []string{"720p", "480p", "360p"},
			height:     "RESOLUTION=720x1280",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clip := filepath.Join(t.TempDir(), "clip.mp4")

			args := append([]string{"-hide_banner", "-loglevel", "error", "-y"}, tc.lavfi...)
			args = append(args, "-t", "9", "-c:v", "libx264", "-preset", "ultrafast", "-shortest", clip)
			require.NoError(t, exec.CommandContext(t.Context(), "ffmpeg", args...).Run())

			store, pub, d := storage.NewMemory(), &fakePub{}, &fakeDelivery{}
			tr := New(Config{
				WorkDir: t.TempDir(), FFmpegPath: "ffmpeg", FFprobePath: "ffprobe", Preset: "ultrafast",
				Bucket: "nxl-uploads", MaxJobs: 1, MaxLive: 1, SegmentSec: 4, MaxDuration: time.Hour,
			}, Deps{
				Queue: &fakeQueue{}, Live: &fakeLive{}, Source: fileSource{path: clip}, Store: store,
				Pub: pub, Rec: &fakeRec{}, Log: logger.New("error"),
			})

			payload := completed(t, nil)
			tr.handle(t.Context(), Message{Event: eventbus.Event{Payload: payload}, Delivered: 1, Delivery: d})

			require.Equal(t, []string{"ack"}, d.got())
			require.Len(t, pub.out, 1)
			require.Equal(t, ev.TypeUploadProcessed, pub.out[0].eventType)

			var out ev.UploadProcessed

			require.NoError(t, json.Unmarshal(pub.out[0].payload, &out))
			assert.Equal(t, tc.renditions, out.Renditions)
			assert.InDelta(t, 9, out.DurationSec, 0.5)

			prefix := "videos/" + out.VODID + "/"
			assert.Equal(t, prefix+"master.m3u8", out.PlaylistPath)
			assert.Equal(t, prefix+"thumb.jpg", out.ThumbnailPath)

			master, ok := store.Get(prefix + "master.m3u8")
			require.True(t, ok)
			assert.Contains(t, string(master.Body), tc.height)

			for _, r := range tc.renditions {
				index, found := store.Get(prefix + r + "/index.m3u8")
				require.True(t, found, r)
				assert.Contains(t, string(index.Body), "#EXT-X-PLAYLIST-TYPE:VOD")
				assert.Contains(t, string(index.Body), "#EXTINF:4.000000,")
				assert.Contains(t, string(index.Body), "#EXT-X-ENDLIST")
			}

			_, ok = store.Get(prefix + "thumb.jpg")
			assert.True(t, ok)

			lastSegment, firstPlaylist := -1, len(store.Order())
			for i, key := range store.Order() {
				switch {
				case strings.HasSuffix(key, ".ts"):
					lastSegment = i
				case strings.HasSuffix(key, ".m3u8"):
					firstPlaylist = min(firstPlaylist, i)
				}
			}

			assert.Less(t, lastSegment, firstPlaylist, "segments are stored before any playlist")

			_, err := os.Stat(tr.jobDir(out.VODID))
			assert.True(t, os.IsNotExist(err), "work dir removed")
		})
	}
}
