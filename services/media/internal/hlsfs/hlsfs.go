// Package hlsfs reads and repairs HLS playlists on disk.
package hlsfs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const endList = "#EXT-X-ENDLIST"

var _segmentNumber = regexp.MustCompile(`seg_(\d+)\.ts$`)

// Segments returns the segment URIs listed in a media playlist, in order.
func Segments(playlist []byte) []string {
	var out []string

	sc := bufio.NewScanner(bytes.NewReader(playlist))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}

	return out
}

// HasEndList reports whether the playlist is closed (VOD).
func HasEndList(playlist []byte) bool {
	return bytes.Contains(playlist, []byte(endList))
}

// LastSegmentNumber returns the highest seg_NNNNN.ts number across the renditions' playlists, 0 when none.
func LastSegmentNumber(dir string, renditions []string) (int, error) {
	last := 0

	for _, r := range renditions {
		data, err := os.ReadFile(filepath.Join(dir, r, "index.m3u8"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return 0, fmt.Errorf("hlsfs - LastSegmentNumber - os.ReadFile: %w", err)
		}

		for _, s := range Segments(data) {
			m := _segmentNumber.FindStringSubmatch(s)
			if m == nil {
				continue
			}

			if n, convErr := strconv.Atoi(m[1]); convErr == nil && n > last {
				last = n
			}
		}
	}

	return last, nil
}

// EnsureEndList appends EXT-X-ENDLIST when an abnormal ffmpeg exit left the playlist open.
func EnsureEndList(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("hlsfs - EnsureEndList - os.ReadFile: %w", err)
	}

	if HasEndList(data) {
		return false, nil
	}

	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}

	data = append(data, endList+"\n"...)

	if err = os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec,mnd // public media file
		return false, fmt.Errorf("hlsfs - EnsureEndList - os.WriteFile: %w", err)
	}

	return true, nil
}

// WithoutEndList returns the playlist with EXT-X-ENDLIST removed (a paused live stream must not look finished).
func WithoutEndList(playlist []byte) []byte {
	if !HasEndList(playlist) {
		return playlist
	}

	var b bytes.Buffer

	sc := bufio.NewScanner(bytes.NewReader(playlist))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == endList {
			continue
		}

		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}

	return b.Bytes()
}

// StripEndList rewrites the file without EXT-X-ENDLIST; missing file is not an error.
func StripEndList(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("hlsfs - StripEndList - os.ReadFile: %w", err)
	}

	if !HasEndList(data) {
		return false, nil
	}

	if err = os.WriteFile(path, WithoutEndList(data), 0o644); err != nil { //nolint:gosec,mnd // public media file
		return false, fmt.Errorf("hlsfs - StripEndList - os.WriteFile: %w", err)
	}

	return true, nil
}

// Duration sums the EXTINF durations of a media playlist.
func Duration(playlist []byte) (float64, error) {
	var total float64

	sc := bufio.NewScanner(bytes.NewReader(playlist))
	for sc.Scan() {
		v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "#EXTINF:")
		if !ok {
			continue
		}

		v, _, _ = strings.Cut(v, ",")

		d, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("hlsfs - Duration: %w", err)
		}

		total += d
	}

	return total, nil
}
