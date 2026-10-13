package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	ev "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/media/internal/hlsfs"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
	"github.com/minhnn/nexuslive-be/services/media/internal/uploader"
)

const (
	_masterName = "master.m3u8"
	_indexName  = "index.m3u8"
	// Recovery uploads whatever the crashed process had not uploaded yet, which can be minutes of video.
	_recoverFlushTimeout = 2 * time.Minute
)

// RecoverResult -.
type RecoverResult struct {
	Recovered int // closed as VOD, events sent
	Discarded int // nothing playable, directory removed
	Kept      int // upload failed, retried on the next start
}

// Recover closes the streams a crashed or killed process left in WorkDir, before any publish is
// accepted: each one is finalized like an expired grace period (ENDLIST, upload, ingest.stopped +
// vod.processed) so stream-svc ends it and creates the VOD. Must run before the RTMP server starts.
func (m *Manager) Recover(ctx context.Context) (RecoverResult, error) {
	var res RecoverResult

	entries, err := os.ReadDir(m.cfg.WorkDir)
	if err != nil {
		return res, fmt.Errorf("session - Recover - read %s: %w", m.cfg.WorkDir, err)
	}

	for _, e := range entries {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}

		// Only directories this service created: their name is a stream UUID.
		if !e.IsDir() || uuid.Validate(e.Name()) != nil {
			continue
		}

		switch m.recoverOne(ctx, e.Name()) {
		case recoverDone:
			res.Recovered++
		case recoverDiscarded:
			res.Discarded++
		case recoverKept:
			res.Kept++
		}
	}

	return res, nil
}

type recoverOutcome int

const (
	recoverDone recoverOutcome = iota
	recoverDiscarded
	recoverKept
)

func (m *Manager) recoverOne(ctx context.Context, id string) recoverOutcome {
	dir := filepath.Join(m.cfg.WorkDir, id)

	playlists := recoverablePlaylists(dir)
	if len(playlists) == 0 {
		m.l.Warn("recover %s: no playable output (crashed while starting), removing", id)

		if err := os.RemoveAll(dir); err != nil {
			m.l.Warn("recover %s: remove dir: %v", id, err)
		}

		return recoverDiscarded
	}

	var duration float64

	for _, pl := range playlists {
		if _, err := hlsfs.EnsureEndList(pl); err != nil {
			m.l.Warn("recover %s: ENDLIST %s: %v", id, pl, err)
		}

		duration = max(duration, playlistDuration(pl))
	}

	flushCtx, cancel := context.WithTimeout(ctx, _recoverFlushTimeout)
	defer cancel()

	if err := uploader.New(m.store, dir, id).Flush(flushCtx); err != nil {
		m.l.Error("recover %s: upload incomplete, keeping %s for the next start: %v", id, dir, err)

		return recoverKept
	}

	if err := os.RemoveAll(dir); err != nil {
		m.l.Warn("recover %s: remove dir: %v", id, err)
	}

	m.l.Info("recover %s: closed after restart, VOD %.1fs", id, duration)
	m.emit(ev.TypeIngestStopped, ev.IngestStopped{StreamID: id, Reason: ev.ReasonRecovered})
	m.emit(ev.TypeVODProcessed, ev.VODProcessed{
		StreamID:      id,
		DurationSec:   duration,
		PlaylistPath:  storage.StreamsPrefix + id + "/" + _masterName,
		ThumbnailPath: storage.StreamsPrefix + id + "/thumb.jpg",
	})

	return recoverDone
}

// recoverablePlaylists returns the rendition playlists with at least one segment, if the master exists.
func recoverablePlaylists(dir string) []string {
	if _, err := os.Stat(filepath.Join(dir, _masterName)); err != nil {
		return nil
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*", _indexName))
	if err != nil {
		return nil
	}

	var out []string

	for _, pl := range matches {
		data, rerr := os.ReadFile(pl)
		if rerr == nil && len(hlsfs.Segments(data)) > 0 {
			out = append(out, pl)
		}
	}

	return out
}
