package imaging_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/imaging"
	"github.com/stretchr/testify/require"
)

func encoded(t *testing.T, w, h int, asPNG bool) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, h/2, color.RGBA{R: 255, A: 255})
	}

	var buf bytes.Buffer
	if asPNG {
		require.NoError(t, png.Encode(&buf, img))
	} else {
		require.NoError(t, jpeg.Encode(&buf, img, nil))
	}

	return buf.Bytes()
}

// pngHeader declares a w×h image without pixel data: only DecodeConfig can read it.
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6

	chunk := append([]byte("IHDR"), ihdr...)
	out := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0, 0, 0, 13}
	out = append(out, chunk...)

	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
}

func TestProcess(t *testing.T) {
	t.Parallel()

	// A 1×1 lossless WebP.
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	require.NoError(t, err)

	avatar := entity.ImageAvatar.Spec(2 << 20)
	banner := entity.ImageBanner.Spec(5 << 20)
	jpg := encoded(t, 400, 300, false)

	tests := []struct {
		name    string
		data    []byte
		spec    entity.ImageSpec
		wantErr error
		w, h    int
	}{
		{name: "jpeg avatar", data: jpg, spec: avatar, w: 256, h: 256},
		{name: "png banner", data: encoded(t, 1000, 400, true), spec: banner, w: 1600, h: 500},
		{name: "text named .jpg", data: []byte("hello, I am not an image at all"), spec: avatar, wantErr: entity.ErrImageFormat},
		{name: "truncated jpeg", data: jpg[:len(jpg)/2], spec: avatar, wantErr: entity.ErrImageFormat},
		{name: "png signature only", data: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, spec: avatar, wantErr: entity.ErrImageFormat},
		{name: "too small", data: encoded(t, 100, 300, true), spec: avatar, wantErr: entity.ErrImageTooSmall},
		{name: "banner too small", data: jpg, spec: banner, wantErr: entity.ErrImageTooSmall},
		{name: "webp is read", data: webp, spec: avatar, wantErr: entity.ErrImageTooSmall},
		{name: "50 megapixels", data: pngHeader(10000, 5000), spec: avatar, wantErr: entity.ErrImageTooLarge},
		{name: "over the byte limit", data: jpg, spec: entity.ImageAvatar.Spec(100), wantErr: entity.ErrImageTooLarge},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := imaging.New().Process(tc.data, tc.spec)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)

			cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
			require.NoError(t, err)
			require.Equal(t, "jpeg", format)
			require.Equal(t, tc.w, cfg.Width)
			require.Equal(t, tc.h, cfg.Height)
		})
	}
}

func TestCover(t *testing.T) {
	t.Parallel()

	a, err := imaging.New().Cover("valorant", 260, 60, 80)
	require.NoError(t, err)

	b, err := imaging.New().Cover("valorant", 260, 60, 80)
	require.NoError(t, err)
	require.Equal(t, a, b, "same slug, same cover")

	cfg, err := jpeg.DecodeConfig(bytes.NewReader(a))
	require.NoError(t, err)
	require.Equal(t, 60, cfg.Width)
}
