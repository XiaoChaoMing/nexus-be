package transcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/media/internal/ffmpegcmd"
)

const (
	_probeTimeout   = 2 * time.Minute
	_quarterTurn    = 90
	_halfTurn       = 2 * _quarterTurn
	_fullTurn       = 360
	_probeErrMaxLen = 512
)

// Probe is what ffprobe reports about an uploaded file. Width and Height are the displayed size
// (rotation applied), as ffmpeg autorotates frames before the filter graph.
type Probe struct {
	DurationSec float64
	Width       int
	Height      int
	FPS         float64
	Rotation    int
	HasAudio    bool
	// VideoStream is the absolute index of the first real video stream (cover art skipped).
	VideoStream int
}

// ShortSide is the side the ladder applies to: the height of a landscape video, the width of a vertical one.
func (p Probe) ShortSide() int {
	return min(p.Width, p.Height)
}

// Portrait reports a video displayed taller than wide.
func (p Probe) Portrait() bool {
	return p.Width < p.Height
}

// Validate rejects a file that cannot become a VOD.
func (p Probe) Validate(maxDuration time.Duration) error {
	if p.VideoStream < 0 {
		return ErrNoVideoStream
	}

	if p.DurationSec <= 0 || p.Width <= 0 || p.Height <= 0 {
		return fmt.Errorf("%w: duration %.3f s, size %dx%d", ErrFileCorrupt, p.DurationSec, p.Width, p.Height)
	}

	if maxDuration > 0 && p.DurationSec > maxDuration.Seconds() {
		return fmt.Errorf("%w: %.0f s", ErrDurationTooLong, p.DurationSec)
	}

	return nil
}

type probeOutput struct {
	Streams []probeStream `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

type probeStream struct {
	Index        int    `json:"index"`
	CodecType    string `json:"codec_type"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"`
	RFrameRate   string `json:"r_frame_rate"`
	Duration     string `json:"duration"`
	Disposition  struct {
		AttachedPic int `json:"attached_pic"`
	} `json:"disposition"`
	Tags struct {
		Rotate string `json:"rotate"`
	} `json:"tags"`
	SideDataList []struct {
		Rotation *float64 `json:"rotation"`
	} `json:"side_data_list"`
}

// ParseProbe reads the JSON of `ffprobe -of json -show_entries format=…:stream=…`.
func ParseProbe(data []byte) (Probe, error) {
	var out probeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return Probe{}, fmt.Errorf("%w: ffprobe output: %w", ErrFileCorrupt, err)
	}

	p := Probe{VideoStream: -1, DurationSec: parseFloat(out.Format.Duration)}

	for _, s := range out.Streams {
		switch s.CodecType {
		case "video":
			if p.VideoStream < 0 && s.Disposition.AttachedPic == 0 {
				p.setVideo(s)
			}
		case "audio":
			p.HasAudio = true
		}
	}

	return p, nil
}

func (p *Probe) setVideo(s probeStream) {
	p.VideoStream = s.Index
	p.Width, p.Height = s.Width, s.Height
	p.Rotation = rotation(s)

	if p.Rotation%_halfTurn != 0 {
		p.Width, p.Height = p.Height, p.Width
	}

	p.FPS = frameRate(s.AvgFrameRate)
	if p.FPS <= 0 {
		p.FPS = frameRate(s.RFrameRate)
	}

	if p.DurationSec <= 0 {
		p.DurationSec = parseFloat(s.Duration)
	}
}

// rotation is the display matrix angle (side data, ffmpeg ≥ 5) or the legacy "rotate" tag, in [0, 360).
func rotation(s probeStream) int {
	deg := 0

	for _, sd := range s.SideDataList {
		if sd.Rotation != nil {
			deg = int(math.Round(*sd.Rotation))

			break
		}
	}

	if deg == 0 && s.Tags.Rotate != "" {
		if v, err := strconv.Atoi(s.Tags.Rotate); err == nil {
			deg = v
		}
	}

	return ((deg % _fullTurn) + _fullTurn) % _fullTurn
}

// frameRate parses "30000/1001" or "25"; "0/0" and garbage read as 0.
func frameRate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return parseFloat(s)
	}

	n, d := parseFloat(num), parseFloat(den)
	if d <= 0 {
		return 0
	}

	return n / d
}

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}

	return f
}

// runProbe runs ffprobe on file, opening it with the same demuxer and protocol whitelists as ffmpeg.
func runProbe(ctx context.Context, ffprobePath, file string) (Probe, error) {
	ctx, cancel := context.WithTimeout(ctx, _probeTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, ffprobePath, //nolint:gosec // the path is built by media-svc, never taken from the event
		"-v", "error",
		"-protocol_whitelist", ffmpegcmd.InputProtocols, "-format_whitelist", ffmpegcmd.InputFormats,
		"-show_entries", "format=duration:stream=index,codec_type,width,height,avg_frame_rate,r_frame_rate,duration"+
			":stream_disposition=attached_pic:stream_tags=rotate:stream_side_data=rotation",
		"-of", "json", file)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Probe{}, fmt.Errorf("transcode - ffprobe: %w", ctx.Err())
		}

		// ffprobe fails when no allowed demuxer recognizes the file.
		return Probe{}, fmt.Errorf("%w: ffprobe: %w (%s)", ErrUnsupportedFormat, err, clip(stderr.String()))
	}

	return ParseProbe(stdout.Bytes())
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > _probeErrMaxLen {
		return s[len(s)-_probeErrMaxLen:]
	}

	return s
}
