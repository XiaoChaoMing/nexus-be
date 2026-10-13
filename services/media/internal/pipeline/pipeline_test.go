package pipeline

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteFLV(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer

	require.NoError(t, writeFLVHeader(&b, true))
	require.Equal(t, []byte{'F', 'L', 'V', 1, 5, 0, 0, 0, 9, 0, 0, 0, 0}, b.Bytes())

	b.Reset()
	require.NoError(t, writeFLVTag(&b, TagVideo, 0x01020304, []byte{0xAA, 0xBB}))
	require.Equal(t, []byte{
		9, 0, 0, 2, // type, size
		0x02, 0x03, 0x04, 0x01, // ts lower 24 bits, extended
		0, 0, 0, // stream id
		0xAA, 0xBB,
		0, 0, 0, 13, // previous tag size
	}, b.Bytes())
}

func TestProgressParser(t *testing.T) {
	t.Parallel()

	var p progressParser

	for _, l := range []string{"frame=120", "fps=30.01", "stream_0_0_q=23.0", "bitrate=N/A", "out_time_us=4000000", "drop_frames=1", "speed=0.98x"} {
		isP, done := p.feed(l)
		require.True(t, isP, l)
		require.Nil(t, done)
	}

	isP, done := p.feed("[hls @ 0x55] Opening 'x' for writing")
	require.False(t, isP)
	require.Nil(t, done)

	isP, done = p.feed("progress=continue")
	require.True(t, isP)
	require.NotNil(t, done)
	require.EqualValues(t, 120, done.Frame)
	require.InDelta(t, 0.98, done.Speed, 1e-9)
	require.InDelta(t, 4.0, done.OutTimeSec, 1e-9)
	require.EqualValues(t, 1, done.DropFrames)
}
