package transcode_test

import (
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/media/internal/transcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	_videoStream = `{"index":0,"codec_type":"video","width":1920,"height":1080,"avg_frame_rate":"60/1","r_frame_rate":"60/1"}`
	_audioStream = `{"index":1,"codec_type":"audio","avg_frame_rate":"0/0","r_frame_rate":"0/0"}`
)

func TestParseProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want transcode.Probe
	}{
		{
			name: "landscape with audio",
			json: `{"streams":[` + _videoStream + `,` + _audioStream + `],"format":{"duration":"10.000000"}}`,
			want: transcode.Probe{DurationSec: 10, Width: 1920, Height: 1080, FPS: 60, HasAudio: true},
		},
		{
			name: "display matrix rotation swaps the displayed size",
			json: `{"streams":[{"index":0,"codec_type":"video","width":1920,"height":1080,"avg_frame_rate":"30000/1001",` +
				`"side_data_list":[{"rotation":-90}]}],"format":{"duration":"5"}}`,
			want: transcode.Probe{DurationSec: 5, Width: 1080, Height: 1920, FPS: 30000.0 / 1001, Rotation: 270},
		},
		{
			name: "legacy rotate tag; avg frame rate unknown",
			json: `{"streams":[{"index":0,"codec_type":"video","width":1280,"height":720,"avg_frame_rate":"0/0",` +
				`"r_frame_rate":"25/1","tags":{"rotate":"90"}}],"format":{"duration":"3.5"}}`,
			want: transcode.Probe{DurationSec: 3.5, Width: 720, Height: 1280, FPS: 25, Rotation: 90},
		},
		{
			name: "upside down keeps the size",
			json: `{"streams":[{"index":0,"codec_type":"video","width":640,"height":480,"avg_frame_rate":"24",` +
				`"side_data_list":[{"rotation":180}]}],"format":{"duration":"1"}}`,
			want: transcode.Probe{DurationSec: 1, Width: 640, Height: 480, FPS: 24, Rotation: 180},
		},
		{
			name: "cover art is not the video stream",
			json: `{"streams":[{"index":0,"codec_type":"video","width":600,"height":600,"disposition":{"attached_pic":1}},` +
				`{"index":1,"codec_type":"video","width":854,"height":480,"avg_frame_rate":"30/1","duration":"7"}],` +
				`"format":{}}`,
			want: transcode.Probe{DurationSec: 7, Width: 854, Height: 480, FPS: 30, VideoStream: 1},
		},
		{
			name: "audio only",
			json: `{"streams":[` + _audioStream + `],"format":{"duration":"60"}}`,
			want: transcode.Probe{DurationSec: 60, HasAudio: true, VideoStream: -1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := transcode.ParseProbe([]byte(tc.json))
			require.NoError(t, err)
			assert.InDelta(t, tc.want.FPS, got.FPS, 1e-9)

			got.FPS, tc.want.FPS = 0, 0
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseProbe_NotJSON(t *testing.T) {
	t.Parallel()

	_, err := transcode.ParseProbe([]byte(`{`))
	require.ErrorIs(t, err, transcode.ErrFileCorrupt)
}

func TestProbeValidate(t *testing.T) {
	t.Parallel()

	ok := transcode.Probe{DurationSec: 60, Width: 1280, Height: 720}

	tests := []struct {
		name  string
		probe transcode.Probe
		max   time.Duration
		want  error
	}{
		{name: "valid", probe: ok, max: time.Hour},
		{name: "no limit", probe: transcode.Probe{DurationSec: 1e6, Width: 2, Height: 2}},
		{name: "no video stream", probe: transcode.Probe{VideoStream: -1, DurationSec: 5}, max: time.Hour, want: transcode.ErrNoVideoStream},
		{name: "no duration", probe: transcode.Probe{Width: 1280, Height: 720}, max: time.Hour, want: transcode.ErrFileCorrupt},
		{name: "no size", probe: transcode.Probe{DurationSec: 5}, max: time.Hour, want: transcode.ErrFileCorrupt},
		{name: "too long", probe: ok, max: 59 * time.Second, want: transcode.ErrDurationTooLong},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.probe.Validate(tc.max)
			if tc.want == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestProbeShortSide(t *testing.T) {
	t.Parallel()

	vertical := transcode.Probe{Width: 1080, Height: 1920}
	assert.Equal(t, 1080, vertical.ShortSide())
	assert.True(t, vertical.Portrait())

	landscape := transcode.Probe{Width: 1920, Height: 1080}
	assert.Equal(t, 1080, landscape.ShortSide())
	assert.False(t, landscape.Portrait())
}

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want transcode.Failure
	}{
		{err: transcode.ErrUnsupportedFormat, want: transcode.Failure{Reason: "UNSUPPORTED_FORMAT"}},
		{err: transcode.ErrNoVideoStream, want: transcode.Failure{Reason: "NO_VIDEO_STREAM"}},
		{err: transcode.ErrDurationTooLong, want: transcode.Failure{Reason: "DURATION_TOO_LONG"}},
		{err: transcode.ErrFileCorrupt, want: transcode.Failure{Reason: "FILE_CORRUPT"}},
		{err: transcode.ErrTranscodeFailed, want: transcode.Failure{Reason: "TRANSCODE_FAILED"}},
		{err: transcode.ErrTimeLimit, want: transcode.Failure{Reason: "TRANSCODE_FAILED", Retryable: true}},
		{err: transcode.ErrStorage, want: transcode.Failure{Reason: "STORAGE_ERROR", Retryable: true}},
		{err: transcode.ErrSourceMissing, want: transcode.Failure{Reason: "STORAGE_ERROR"}},
		{err: transcode.ErrBadSource, want: transcode.Failure{Reason: "STORAGE_ERROR"}},
		{err: assert.AnError, want: transcode.Failure{Reason: "TRANSCODE_FAILED"}},
	}

	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, transcode.Classify(tc.err))
		})
	}
}
