package ffmpegcmd_test

import (
	"strings"
	"testing"

	"github.com/minhnn/nexuslive-be/services/media/internal/ffmpegcmd"
	"github.com/minhnn/nexuslive-be/services/media/internal/ladder"
	"github.com/stretchr/testify/assert"
)

func value(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}

	return ""
}

func TestBuild_FullLadderWithAudio(t *testing.T) {
	t.Parallel()

	args := ffmpegcmd.Build(ffmpegcmd.Options{Renditions: ladder.For(1080), HasAudio: true})
	joined := strings.Join(args, " ")

	assert.Equal(t, "[0:v]fps=30,split=4[s0][s1][s2][st];[s0]scale=-2:720[v0];[s1]scale=-2:480[v1];[s2]scale=-2:360[v2];[st]fps=1/5,scale=320:-2[th3]",
		value(args, "-filter_complex"))
	assert.Equal(t, "v:0,a:0,name:720p v:1,a:1,name:480p v:2,a:2,name:360p", value(args, "-var_stream_map"))
	assert.Equal(t, "event", value(args, "-hls_playlist_type"))
	assert.Equal(t, "60", value(args, "-g"), "keyframe every 2s at 30 fps")
	assert.Equal(t, "1", value(args, "-start_number"))
	assert.NotContains(t, value(args, "-hls_flags"), "append_list")
	assert.Equal(t, 3, strings.Count(joined, "-map 0:a:0"))
	assert.Contains(t, joined, "-b:v:2 700k")
	assert.Contains(t, joined, "-b:a:2 96k")
	assert.Equal(t, "thumb.jpg", args[len(args)-1])
}

func TestBuild_ResumeWithoutAudio(t *testing.T) {
	t.Parallel()

	args := ffmpegcmd.Build(ffmpegcmd.Options{Renditions: ladder.For(480), StartNumber: 42, Append: true})
	joined := strings.Join(args, " ")

	assert.Equal(t, "v:0,name:480p v:1,name:360p", value(args, "-var_stream_map"))
	assert.Equal(t, "42", value(args, "-start_number"))
	assert.Contains(t, value(args, "-hls_flags"), "append_list")
	assert.NotContains(t, joined, "0:a")
	assert.NotContains(t, joined, "-c:a")
}
