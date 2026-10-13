package pipeline

import (
	"strconv"
	"strings"
	"time"
)

// Progress is the last block reported by `ffmpeg -progress`.
type Progress struct {
	Frame      int64     `json:"frame"`
	FPS        float64   `json:"fps"`
	Bitrate    string    `json:"bitrate"`
	DropFrames int64     `json:"dropFrames"`
	DupFrames  int64     `json:"dupFrames"`
	Speed      float64   `json:"speed"`
	OutTimeSec float64   `json:"outTimeSec"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// progressParser accumulates key=value lines until a `progress=` line closes the block.
type progressParser struct {
	cur Progress
}

// feed returns true when line was a progress key (so it should not be logged as ffmpeg output).
func (p *progressParser) feed(line string) (bool, *Progress) {
	key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok || strings.ContainsAny(key, " \t[") {
		return false, nil
	}

	if key == "progress" {
		done := p.cur
		done.UpdatedAt = time.Now()

		return true, &done
	}

	if p.set(key, strings.TrimSpace(val)) {
		return true, nil
	}

	// stream_0_0_q, total_size, out_time, ... are known progress keys we ignore.
	return isProgressKey(key), nil
}

// set stores one value of the current block; false when key is not one we keep.
func (p *progressParser) set(key, val string) bool {
	switch key {
	case "frame":
		p.cur.Frame = intOrZero(val)
	case "fps":
		p.cur.FPS = floatOrZero(val)
	case "bitrate":
		p.cur.Bitrate = val
	case "drop_frames":
		p.cur.DropFrames = intOrZero(val)
	case "dup_frames":
		p.cur.DupFrames = intOrZero(val)
	case "speed":
		p.cur.Speed = floatOrZero(strings.TrimSuffix(val, "x"))
	case "out_time_us", "out_time_ms": // both are microseconds in ffmpeg
		if us, err := strconv.ParseInt(val, 10, 64); err == nil {
			p.cur.OutTimeSec = float64(us) / float64(time.Second/time.Microsecond)
		}
	default:
		return false
	}

	return true
}

// intOrZero parses an ffmpeg counter; values such as "N/A" (before the first frame) read as 0.
func intOrZero(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}

	return n
}

// floatOrZero is intOrZero for decimal values (fps, speed).
func floatOrZero(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}

	return f
}

func isProgressKey(key string) bool {
	if strings.HasPrefix(key, "stream_") {
		return true
	}

	switch key {
	case "total_size", "out_time", "dup_frames", "drop_frames":
		return true
	}

	return false
}
