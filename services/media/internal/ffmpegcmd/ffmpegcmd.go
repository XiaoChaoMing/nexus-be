// Package ffmpegcmd builds the ffmpeg arguments for one live stream (TSD §5.3).
package ffmpegcmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/minhnn/nexuslive-be/services/media/internal/entity"
)

const (
	_fps            = 30
	_segmentSeconds = 2
	// One keyframe per segment so every rendition cuts segments at the same instant.
	_gop            = _fps * _segmentSeconds
	_thumbWidth     = 320
	_thumbEverySec  = 5
	_probeMicrosecs = 2_000_000
	_muxQueue       = 1024
)

// FirstSegmentNumber is the number of seg_00001.ts, the first segment of every stream.
const FirstSegmentNumber = 1

// Options -.
type Options struct {
	Renditions []entity.Rendition
	HasAudio   bool
	Preset     string
	// StartNumber becomes EXT-X-MEDIA-SEQUENCE and the first file number. Keep it unchanged on resume:
	// with Append, ffmpeg continues numbering after the segments already in the playlist.
	StartNumber int
	// Append keeps the existing playlists and continues them (adds EXT-X-DISCONTINUITY).
	Append bool
}

// Build returns the arguments (without the binary). The process must run with the stream directory as its
// working directory: every output path is relative.
func Build(o Options) []string {
	n := len(o.Renditions)
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats", "-progress", "pipe:2",
		"-analyzeduration", strconv.Itoa(_probeMicrosecs), "-probesize", strconv.Itoa(_probeMicrosecs),
		"-f", "flv", "-i", "pipe:0",
		"-filter_complex", filterGraph(o.Renditions),
	}

	for i := range o.Renditions {
		args = append(args, "-map", fmt.Sprintf("[v%d]", i))
		if o.HasAudio {
			args = append(args, "-map", "0:a:0")
		}
	}

	args = append(args,
		"-c:v", "libx264", "-preset", orDefault(o.Preset, "veryfast"), "-tune", "zerolatency",
		"-profile:v", "main", "-pix_fmt", "yuv420p",
		"-g", strconv.Itoa(_gop), "-keyint_min", strconv.Itoa(_gop), "-sc_threshold", "0",
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", _segmentSeconds),
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

	flags := "independent_segments+temp_file+program_date_time"
	if o.Append {
		flags += "+append_list"
	}

	start := o.StartNumber
	if start < FirstSegmentNumber {
		start = FirstSegmentNumber
	}

	args = append(args,
		"-max_muxing_queue_size", strconv.Itoa(_muxQueue),
		"-f", "hls",
		"-hls_time", strconv.Itoa(_segmentSeconds),
		// EVENT playlists only grow: they give viewers DVR while live and become the VOD once ENDLIST is written.
		"-hls_playlist_type", "event",
		"-hls_flags", flags,
		"-hls_segment_type", "mpegts",
		"-start_number", strconv.Itoa(start),
		"-hls_segment_filename", "%v/seg_%05d.ts",
		"-master_pl_name", "master.m3u8",
		"-var_stream_map", varStreamMap(o.Renditions, o.HasAudio),
		"%v/index.m3u8",
		// Thumbnail output.
		"-map", fmt.Sprintf("[th%d]", n), "-update", "1", "-q:v", "5", "-y", "thumb.jpg",
	)

	return args
}

func filterGraph(rs []entity.Rendition) string {
	n := len(rs)

	var b strings.Builder

	fmt.Fprintf(&b, "[0:v]fps=%d,split=%d", _fps, n+1)

	for i := range rs {
		fmt.Fprintf(&b, "[s%d]", i)
	}

	fmt.Fprintf(&b, "[st];")

	for i, r := range rs {
		fmt.Fprintf(&b, "[s%d]scale=-2:%d[v%d];", i, r.Height, i)
	}

	fmt.Fprintf(&b, "[st]fps=1/%d,scale=%d:-2[th%d]", _thumbEverySec, _thumbWidth, n)

	return b.String()
}

func varStreamMap(rs []entity.Rendition, hasAudio bool) string {
	parts := make([]string, 0, len(rs))

	for i, r := range rs {
		if hasAudio {
			parts = append(parts, fmt.Sprintf("v:%d,a:%d,name:%s", i, i, r.Name))
		} else {
			parts = append(parts, fmt.Sprintf("v:%d,name:%s", i, r.Name))
		}
	}

	return strings.Join(parts, " ")
}

func kbps(v int) string {
	return strconv.Itoa(v) + "k"
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}
