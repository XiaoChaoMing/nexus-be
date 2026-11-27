package ffmpegcmd_test

import (
	"strings"
	"testing"

	"github.com/minhnn/nexuslive-be/services/media/internal/ffmpegcmd"
	"github.com/minhnn/nexuslive-be/services/media/internal/ladder"
	"github.com/stretchr/testify/assert"
)

func TestBuildVOD(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	tests := []struct {
		name       string
		opts       ffmpegcmd.VODOptions
		filter     string
		streamMap  string
		gop        string
		audioMaps  int
		withAudio  bool
		thumbSeek  string
		segmentSec string
	}{
		{
			name: "landscape 1080p with audio",
			opts: ffmpegcmd.VODOptions{
				Input: "../source", Renditions: ladder.For(1080), HasAudio: true, FPS: 30, ThumbAtSec: 12.5,
			},
			filter: "[0:0]fps=30,split=3[s0][s1][s2];[s0]scale=-2:720[v0];[s1]scale=-2:480[v1];" +
				"[s2]scale=-2:360[v2];[1:0]scale=640:-2[th3]",
			streamMap: "v:0,a:0,name:720p v:1,a:1,name:480p v:2,a:2,name:360p",
			gop:       "120", audioMaps: 3, withAudio: true, thumbSeek: "12.500", segmentSec: "4",
		},
		{
			name: "vertical video scales the short side (width)",
			opts: ffmpegcmd.VODOptions{
				Input: "../source", Renditions: ladder.For(1080), HasAudio: true, Portrait: true, FPS: 30,
			},
			filter: "[0:0]fps=30,split=3[s0][s1][s2];[s0]scale=720:-2[v0];[s1]scale=480:-2[v1];" +
				"[s2]scale=360:-2[v2];[1:0]scale=640:-2[th3]",
			streamMap: "v:0,a:0,name:720p v:1,a:1,name:480p v:2,a:2,name:360p",
			gop:       "120", audioMaps: 3, withAudio: true, thumbSeek: "0.000", segmentSec: "4",
		},
		{
			name: "no audio: no audio map nor encoder",
			opts: ffmpegcmd.VODOptions{
				Input: "../source", VideoStream: 1, Renditions: ladder.For(480), FPS: 25, SegmentSec: 6,
			},
			filter:    "[0:1]fps=25,split=2[s0][s1];[s0]scale=-2:480[v0];[s1]scale=-2:360[v1];[1:1]scale=640:-2[th2]",
			streamMap: "v:0,name:480p v:1,name:360p",
			gop:       "150", thumbSeek: "0.000", segmentSec: "6",
		},
		{
			name: "60 fps source capped to 30",
			opts: ffmpegcmd.VODOptions{
				Input: "../source", Renditions: ladder.For(720), HasAudio: true, FPS: 60,
			},
			filter: "[0:0]fps=30,split=3[s0][s1][s2];[s0]scale=-2:720[v0];[s1]scale=-2:480[v1];" +
				"[s2]scale=-2:360[v2];[1:0]scale=640:-2[th3]",
			streamMap: "v:0,a:0,name:720p v:1,a:1,name:480p v:2,a:2,name:360p",
			gop:       "120", audioMaps: 3, withAudio: true, thumbSeek: "0.000", segmentSec: "4",
		},
		{
			name:      "unknown fps becomes 30",
			opts:      ffmpegcmd.VODOptions{Input: "../source", Renditions: ladder.For(360)},
			filter:    "[0:0]fps=30,split=1[s0];[s0]scale=-2:360[v0];[1:0]scale=640:-2[th1]",
			streamMap: "v:0,name:360p",
			gop:       "120", thumbSeek: "0.000", segmentSec: "4",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := ffmpegcmd.BuildVOD(tc.opts)
			joined := strings.Join(args, " ")

			assert.Equal(t, tc.filter, value(args, "-filter_complex"))
			assert.Equal(t, tc.streamMap, value(args, "-var_stream_map"))
			assert.Equal(t, tc.gop, value(args, "-g"))
			assert.Equal(t, tc.segmentSec, value(args, "-hls_time"))
			assert.Equal(t, "expr:gte(t,n_forced*"+tc.segmentSec+")", value(args, "-force_key_frames"))
			assert.Equal(t, tc.thumbSeek, value(args, "-ss"))
			assert.Equal(t, tc.audioMaps, strings.Count(joined, "-map 0:a:0"))
			assert.Equal(t, tc.withAudio, strings.Contains(joined, "-c:a aac"))

			assert.Equal(t, "vod", value(args, "-hls_playlist_type"))
			assert.Equal(t, "independent_segments", value(args, "-hls_flags"))
			assert.Equal(t, "mpegts", value(args, "-hls_segment_type"))
			assert.Equal(t, "veryfast", value(args, "-preset"))
			assert.NotContains(t, joined, "zerolatency")
			assert.Equal(t, 2, strings.Count(joined, "-protocol_whitelist file,pipe -format_whitelist mov,matroska"),
				"both openings of the file are restricted")
			assert.Equal(t, 2, strings.Count(joined, "-i ../source"))
			assert.Equal(t, "thumb.jpg", args[len(args)-1])
		})
	}
}
