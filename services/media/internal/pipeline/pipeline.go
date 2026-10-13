// Package pipeline runs one ffmpeg process per stream and feeds it FLV from RTMP.
package pipeline

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/ffmpegcmd"
)

const (
	_stdinBuffer   = 64 << 10 // FLV written to ffmpeg
	_stderrBuffer  = 64 << 10
	_stderrMaxLine = 1 << 20
	_stopTimeout   = 5 * time.Second
	_slowSpeed     = 0.9
	_slowWarnAfter = 15 * time.Second
)

// ErrExited is returned by WriteTag once ffmpeg is gone.
var ErrExited = errors.New("pipeline: ffmpeg exited")

// Pipeline -.
type Pipeline struct {
	l     logger.Interface
	label string
	cmd   *exec.Cmd
	stdin io.WriteCloser
	w     *bufio.Writer

	writeMu sync.Mutex
	closed  bool

	done    chan struct{}
	waitErr error

	progMu    sync.Mutex
	progress  Progress
	slowSince time.Time
}

// Start launches ffmpeg in dir (all HLS paths in the command are relative) and writes the FLV header.
func Start(l logger.Interface, label, ffmpegPath, dir string, o ffmpegcmd.Options) (*Pipeline, error) {
	cmd := exec.CommandContext(context.Background(), ffmpegPath, ffmpegcmd.Build(o)...) //nolint:gosec // args built from config + ladder, never from client input
	cmd.Dir = dir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pipeline - StdinPipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("pipeline - StderrPipe: %w", err)
	}

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("pipeline - start ffmpeg: %w", err)
	}

	p := &Pipeline{
		l:     l,
		label: label,
		cmd:   cmd,
		stdin: stdin,
		w:     bufio.NewWriterSize(stdin, _stdinBuffer),
		done:  make(chan struct{}),
	}

	go p.readStderr(stderr)

	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()

	if err = writeFLVHeader(p.w, o.HasAudio); err != nil {
		p.kill()

		return nil, fmt.Errorf("pipeline - write FLV header: %w", err)
	}

	return p, nil
}

// WriteTag forwards one RTMP audio/video/script message. It blocks when ffmpeg is slow (back-pressure to OBS).
func (p *Pipeline) WriteTag(tagType byte, timestamp uint32, body []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	if p.closed {
		return ErrExited
	}

	select {
	case <-p.done:
		return ErrExited
	default:
	}

	if err := writeFLVTag(p.w, tagType, timestamp, body); err != nil {
		return fmt.Errorf("%w: %w", ErrExited, err)
	}

	// Flush on video so ffmpeg sees frames promptly; audio rides along.
	if tagType == TagVideo {
		if err := p.w.Flush(); err != nil {
			return fmt.Errorf("%w: %w", ErrExited, err)
		}
	}

	return nil
}

// Done is closed when ffmpeg exits.
func (p *Pipeline) Done() <-chan struct{} {
	return p.done
}

// Err is ffmpeg's exit error; valid after Done.
func (p *Pipeline) Err() error {
	return p.waitErr
}

// Progress -.
func (p *Pipeline) Progress() Progress {
	p.progMu.Lock()
	defer p.progMu.Unlock()

	return p.progress
}

// Stop closes stdin so ffmpeg finalizes playlists (ENDLIST) and exits; kills it after 5s.
// Returns true when ffmpeg exited cleanly.
func (p *Pipeline) Stop() bool {
	p.writeMu.Lock()
	if !p.closed {
		p.closed = true
		// ffmpeg may already be gone: nothing to do about either error.
		_ = p.w.Flush()
		_ = p.stdin.Close()
	}
	p.writeMu.Unlock()

	select {
	case <-p.done:
	case <-time.After(_stopTimeout):
		p.l.Warn("pipeline %s: ffmpeg did not exit within %s, killing", p.label, _stopTimeout)
		p.kill()
		<-p.done

		return false
	}

	return p.waitErr == nil
}

func (p *Pipeline) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill() //nolint:errcheck // best effort
	}
}

func (p *Pipeline) readStderr(r io.Reader) {
	var pp progressParser

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, _stderrBuffer), _stderrMaxLine)

	for sc.Scan() {
		line := sc.Text()

		isProgress, done := pp.feed(line)
		if done != nil {
			p.onProgress(*done)
		}

		if !isProgress && line != "" {
			p.l.Warn("ffmpeg %s: %s", p.label, line)
		}
	}
}

func (p *Pipeline) onProgress(pr Progress) {
	p.progMu.Lock()
	defer p.progMu.Unlock()

	p.progress = pr

	if pr.Speed > 0 && pr.Speed < _slowSpeed {
		if p.slowSince.IsZero() {
			p.slowSince = pr.UpdatedAt
		} else if pr.UpdatedAt.Sub(p.slowSince) >= _slowWarnAfter {
			p.l.Warn("pipeline %s: CPU cannot keep up, speed=%.2fx for %s", p.label, pr.Speed, _slowWarnAfter)
			p.slowSince = pr.UpdatedAt
		}
	} else {
		p.slowSince = time.Time{}
	}
}
