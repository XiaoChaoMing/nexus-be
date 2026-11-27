package transcode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/minhnn/nexuslive-be/services/media/internal/pipeline"
)

const (
	_stderrMaxLine = 1 << 20
	_stderrBuffer  = 64 << 10
	_tailLines     = 5
)

// runFFmpeg runs ffmpeg at a lower priority in dir, reporting `-progress` output time (s) and speed. On
// failure the error carries the last lines ffmpeg printed.
func (t *Transcoder) runFFmpeg(ctx context.Context, dir string, args []string, onProgress func(outSec, speed float64)) error {
	bin, argv := lowPriority(t.cfg.FFmpegPath, args)

	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Dir = dir

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("transcode - StderrPipe: %w", err)
	}

	if err = cmd.Start(); err != nil {
		return fmt.Errorf("%w: start: %w", ErrTranscodeFailed, err)
	}

	var (
		reader pipeline.ProgressReader
		tail   []string
	)

	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, _stderrBuffer), _stderrMaxLine)

	for sc.Scan() {
		line := sc.Text()

		if p := reader.Feed(line); p != nil {
			onProgress(p.OutTimeSec, p.Speed)

			continue
		}

		if isLogLine(line) {
			tail = append(tail, line)
			if len(tail) > _tailLines {
				tail = tail[1:]
			}
		}
	}

	// A line longer than the scanner allows stops it; keep draining so ffmpeg never blocks on stderr.
	_, _ = io.Copy(io.Discard, stderr) //nolint:errcheck // only drains the pipe

	if err = cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("transcode - ffmpeg: %w", ctx.Err())
		}

		return fmt.Errorf("%w: %w: %s", ErrTranscodeFailed, err, strings.Join(tail, " | "))
	}

	return nil
}

// isLogLine tells ffmpeg's messages from `-progress` key=value lines.
func isLogLine(line string) bool {
	key, _, ok := strings.Cut(strings.TrimSpace(line), "=")

	return line != "" && (!ok || strings.ContainsAny(key, " \t["))
}
