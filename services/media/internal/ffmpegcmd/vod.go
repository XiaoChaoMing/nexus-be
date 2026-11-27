package ffmpegcmd

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/minhnn/nexuslive-be/services/media/internal/entity"
)

const (
	// MaxVODFPS caps the frame rate of an uploaded video's renditions.
	MaxVODFPS        = 30
	_vodThumbWidth   = 640
	_vodThumbQuality = 3
	_vodSegmentSec   = 4
	// InputFormats are the only demuxers ffprobe and ffmpeg may open for an uploaded file (MP4/MOV,
	// MKV/WebM): a playlist or concat file disguised as a video could otherwise make ffmpeg read other files.
	InputFormats = "mov,matroska"
	// InputProtocols keeps ffmpeg to local files: no network fetches from a crafted container.
	InputProtocols = "file,pipe"
)

// VODOptions describe the transcoding of one uploaded file into an HLS VOD.
type VODOptions struct {
	// Input is the source file path.
	Input string
	// VideoStream is the absolute index of the source's video stream (cover art is not a candidate).
	VideoStream int
	Renditions  []entity.Rendition
	HasAudio    bool
	// Portrait: the displayed width is smaller than the height, so a rendition's height applies to the width
	// (the short side) and a 1080x1920 source gives 720x1280, not 404x720.
	Portrait bool
	// FPS of the output; 0 or anything above MaxVODFPS becomes MaxVODFPS.
	FPS        float64
	SegmentSec int
	Preset     string
	// ThumbAtSec is where the thumbnail frame is taken.
	ThumbAtSec float64
}

// BuildVOD returns the arguments (without the binary) for an uploaded file. Like Build, every output path is
// relative: the process runs with the output directory as its working directory.
func BuildVOD(o VODOptions) []string {
	seg := o.SegmentSec
	if seg <= 0 {
		seg = _vodSegmentSec
	}

	fps := vodFPS(o.FPS)
	gop := strconv.Itoa(int(math.Round(fps * float64(seg))))
	n := len(o.Renditions)

	args := make([]string, 0, len(o.Renditions)*8+64) //nolint:mnd // capacity hint only
	args = append(args,
		"-hide_banner", "-nostdin", "-y", "-loglevel", "warning", "-nostats", "-progress", "pipe:2",
		"-protocol_whitelist", InputProtocols, "-format_whitelist", InputFormats, "-i", o.Input,
		// A fast seek on a second opening of the file is much cheaper than decoding up to the thumbnail.
		"-protocol_whitelist", InputProtocols, "-format_whitelist", InputFormats,
		"-ss", strconv.FormatFloat(math.Max(o.ThumbAtSec, 0), 'f', 3, 64), "-i", o.Input,
		"-filter_complex", vodFilterGraph(o, fps),
	)

	for i := range o.Renditions {
		args = append(args, "-map", fmt.Sprintf("[v%d]", i))
		if o.HasAudio {
			args = append(args, "-map", "0:a:0")
		}
	}

	args = append(args,
		"-c:v", "libx264", "-preset", orDefault(o.Preset, "veryfast"),
		"-profile:v", "main", "-pix_fmt", "yuv420p",
		"-g", gop, "-keyint_min", gop, "-sc_threshold", "0",
		// Forced keyframes on the segment grid make every segment exactly SegmentSec long in every rendition.
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", seg),
	)

	for i, r := range o.Renditions {
		args = append(args,
			fmt.Sprintf("-b:v:%d", i), kbps(r.VideoKbps),
			fmt.Sprintf("-maxrate:v:%d", i), kbps(r.MaxrateKbps),
			fmt.Sprintf("-bufsize:v:%d", i), kbps(r.BufsizeKbps),
		)
	}

	if o.HasAudio {
		args = append(args, "-c:a", "aac", "-ar", "48000", "-ac", "2")
		for i, r := range o.Renditions {
			args = append(args, fmt.Sprintf("-b:a:%d", i), kbps(r.AudioKbps))
		}
	}

	return append(args,
		"-max_muxing_queue_size", strconv.Itoa(_muxQueue),
		"-f", "hls",
		"-hls_time", strconv.Itoa(seg),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments",
		"-hls_segment_type", "mpegts",
		"-start_number", strconv.Itoa(FirstSegmentNumber),
		"-hls_segment_filename", "%v/seg_%05d.ts",
		"-master_pl_name", "master.m3u8",
		"-var_stream_map", varStreamMap(o.Renditions, o.HasAudio),
		"%v/index.m3u8",
		"-map", fmt.Sprintf("[th%d]", n), "-frames:v", "1", "-update", "1", "-q:v", strconv.Itoa(_vodThumbQuality),
		"thumb.jpg",
	)
}

func vodFPS(fps float64) float64 {
	if fps <= 0 || fps > MaxVODFPS {
		return MaxVODFPS
	}

	return fps
}

func vodFilterGraph(o VODOptions, fps float64) string {
	n := len(o.Renditions)

	var b strings.Builder

	fmt.Fprintf(&b, "[0:%d]fps=%s,split=%d", o.VideoStream, strconv.FormatFloat(fps, 'f', -1, 64), n)

	for i := range o.Renditions {
		fmt.Fprintf(&b, "[s%d]", i)
	}

	b.WriteString(";")

	for i, r := range o.Renditions {
		if o.Portrait {
			fmt.Fprintf(&b, "[s%d]scale=%d:-2[v%d];", i, r.Height, i)
		} else {
			fmt.Fprintf(&b, "[s%d]scale=-2:%d[v%d];", i, r.Height, i)
		}
	}

	fmt.Fprintf(&b, "[1:%d]scale=%d:-2[th%d]", o.VideoStream, _vodThumbWidth, n)

	return b.String()
}
