// Package imaging turns an uploaded avatar or banner into a JPEG of a fixed size, and draws the
// placeholder covers of categories. It knows nothing of channels.
package imaging

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

const (
	_jpegQuality = 85
	_webpHeader  = 12
	_rgbMax      = 255
	_hueTurn     = 360
	_hueSector   = 60
	_byteBits    = 8
	// Gradient endpoints: a bright color fading to a darker one.
	_satFrom, _valFrom = 0.65, 0.85
	_satTo, _valTo     = 0.75, 0.35
)

type decoder struct {
	config func(io.Reader) (image.Config, error)
	decode func(io.Reader) (image.Image, error)
}

// sniff recognizes JPEG, PNG and WebP by their signature; the file name and declared type are ignored.
func sniff(data []byte) (decoder, bool) {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return decoder{config: jpeg.DecodeConfig, decode: jpeg.Decode}, true
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return decoder{config: png.DecodeConfig, decode: png.Decode}, true
	case len(data) >= _webpHeader && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return decoder{config: webp.DecodeConfig, decode: webp.Decode}, true
	}

	return decoder{}, false
}

// Processor implements the use case's image port.
type Processor struct{}

// New -.
func New() Processor { return Processor{} }

// Process checks data against spec and returns it center-cropped to the spec's ratio, scaled to its size
// and encoded as JPEG (metadata such as EXIF is not carried over).
func (Processor) Process(data []byte, spec entity.ImageSpec) ([]byte, error) {
	if spec.MaxBytes > 0 && len(data) > spec.MaxBytes {
		return nil, entity.ErrImageTooLarge
	}

	src, err := decode(data, spec)
	if err != nil {
		return nil, err
	}

	dst := image.NewRGBA(image.Rect(0, 0, spec.Width, spec.Height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, cropRect(src.Bounds(), spec.Width, spec.Height), draw.Src, nil)

	var out bytes.Buffer
	if err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: _jpegQuality}); err != nil {
		return nil, fmt.Errorf("imaging - encode: %w", err)
	}

	return out.Bytes(), nil
}

// decode checks the signature and the declared size before decoding the pixels.
func decode(data []byte, spec entity.ImageSpec) (image.Image, error) {
	dec, ok := sniff(data)
	if !ok {
		return nil, entity.ErrImageFormat
	}

	cfg, err := dec.config(bytes.NewReader(data))
	if err != nil {
		return nil, entity.ErrImageFormat
	}

	// Checked before decoding, so a small file declaring a huge canvas cannot exhaust memory.
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > entity.ImageMaxPixels {
		return nil, entity.ErrImageTooLarge
	}

	if cfg.Width < spec.MinWidth || cfg.Height < spec.MinHeight {
		return nil, entity.ErrImageTooSmall
	}

	src, err := dec.decode(bytes.NewReader(data))
	if err != nil {
		return nil, entity.ErrImageFormat
	}

	return src, nil
}

// cropRect is the largest rectangle of b with the ratio w:h, centered.
func cropRect(b image.Rectangle, w, h int) image.Rectangle {
	bw, bh := b.Dx(), b.Dy()

	if bw*h > bh*w { // wider than the target: cut the sides
		cw := bh * w / h
		x := b.Min.X + (bw-cw)/2 //nolint:mnd // center

		return image.Rect(x, b.Min.Y, x+cw, b.Max.Y)
	}

	ch := bw * h / w
	y := b.Min.Y + (bh-ch)/2 //nolint:mnd // center

	return image.Rect(b.Min.X, y, b.Max.X, y+ch)
}

// Cover draws a w×h diagonal gradient whose two colors are derived from seed (a category slug), so every
// category keeps the same placeholder; baseHue tints it by group.
func (Processor) Cover(seed string, baseHue, w, h int) ([]byte, error) {
	f := fnv.New32a()
	_, _ = f.Write([]byte(seed)) // hash.Hash.Write never fails
	sum := f.Sum32()

	from := hsv(baseHue+int(sum%_hueSector), _satFrom, _valFrom)
	to := hsv(baseHue+int((sum>>_byteBits)%_hueSector)+_hueSector, _satTo, _valTo)

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	span := float64(w + h)

	for y := range h {
		for x := range w {
			t := float64(x+y) / span
			img.SetRGBA(x, y, color.RGBA{R: mix(from.R, to.R, t), G: mix(from.G, to.G, t), B: mix(from.B, to.B, t), A: _rgbMax})
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: _jpegQuality}); err != nil {
		return nil, fmt.Errorf("imaging - cover: %w", err)
	}

	return out.Bytes(), nil
}

func mix(a, b uint8, t float64) uint8 {
	return uint8(float64(a)*(1-t) + float64(b)*t)
}

// hsv converts a hue in degrees and saturation / value in [0, 1] to RGB.
func hsv(hue int, s, v float64) color.RGBA {
	hue = ((hue % _hueTurn) + _hueTurn) % _hueTurn
	c := v * s
	hp := float64(hue) / _hueSector
	x := c * (1 - abs(mod2(hp)-1))
	m := v - c

	sectors := [6][3]float64{{c, x, 0}, {x, c, 0}, {0, c, x}, {0, x, c}, {x, 0, c}, {c, 0, x}}
	rgb := sectors[min(int(hp), len(sectors)-1)]

	return color.RGBA{R: channel(rgb[0] + m), G: channel(rgb[1] + m), B: channel(rgb[2] + m), A: _rgbMax}
}

func channel(f float64) uint8 {
	return uint8(max(0, min(f, 1)) * _rgbMax)
}

func mod2(f float64) float64 {
	for f >= 2 {
		f -= 2
	}

	return f
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}

	return f
}
