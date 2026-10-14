package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
)

var _ repo.ViewerRepo = (*Repo)(nil)

// CreatePlaybackSession -.
func (r *Repo) CreatePlaybackSession(ctx context.Context, s entity.PlaybackSession) error {
	_, err := r.Pool.Exec(ctx,
		`INSERT INTO playback_sessions (id, stream_id, vod_id, user_id, anon_id, mature_acknowledged, created_at, expires_at)
		 VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, ''), $6, $7, $8)`,
		s.ID, s.Target.StreamID, s.Target.VODID, s.UserID, s.AnonID, s.MatureAcknowledged, s.CreatedAt, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("Repo - CreatePlaybackSession: %w", err)
	}

	return nil
}

// HasMatureConsent -.
func (r *Repo) HasMatureConsent(ctx context.Context, userID, channelID string) (bool, error) {
	var ok bool

	err := r.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM mature_consents WHERE user_id = $1 AND channel_id = $2)`, userID, channelID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("Repo - HasMatureConsent: %w", err)
	}

	return ok, nil
}

// SaveMatureConsent -.
func (r *Repo) SaveMatureConsent(ctx context.Context, userID, channelID string) error {
	_, err := r.Pool.Exec(ctx,
		`INSERT INTO mature_consents (user_id, channel_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, channelID)
	if err != nil {
		return fmt.Errorf("Repo - SaveMatureConsent: %w", err)
	}

	return nil
}

// SaveProgress -.
func (r *Repo) SaveProgress(ctx context.Context, p entity.WatchProgress, keepFurther bool) error {
	_, err := r.Pool.Exec(ctx,
		`INSERT INTO watch_progress (user_id, vod_id, position_sec, updated_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, vod_id) DO UPDATE
		 SET position_sec = CASE WHEN $5 THEN GREATEST(watch_progress.position_sec, EXCLUDED.position_sec)
		                         ELSE EXCLUDED.position_sec END,
		     updated_at = EXCLUDED.updated_at`,
		p.UserID, p.VODID, p.PositionSec, p.UpdatedAt, keepFurther)
	if err != nil {
		return fmt.Errorf("Repo - SaveProgress: %w", err)
	}

	return nil
}

// Progress -.
func (r *Repo) Progress(ctx context.Context, userID, vodID string) (*int, error) {
	var pos int

	err := r.Pool.QueryRow(ctx,
		`SELECT position_sec FROM watch_progress WHERE user_id = $1 AND vod_id = $2`, userID, vodID).Scan(&pos)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // "never watched" is not an error
	}

	if err != nil {
		return nil, fmt.Errorf("Repo - Progress: %w", err)
	}

	return &pos, nil
}

// ContinueWatching -.
func (r *Repo) ContinueWatching(ctx context.Context, userID string, limit int) ([]entity.ContinueItem, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT v.id::text, v.stream_id::text, v.channel_id::text, v.title, COALESCE(v.duration_sec, 0),
		        COALESCE(v.playlist_path, ''), COALESCE(v.thumbnail_url, ''), v.status, v.created_at, v.expires_at,
		        w.position_sec, w.updated_at
		 FROM watch_progress w JOIN vods v ON v.id = w.vod_id
		 WHERE w.user_id = $1 AND v.status = 'READY' AND v.visibility = 'PUBLIC' AND v.expires_at > now()
		   AND w.position_sec >= $2
		   AND (v.duration_sec IS NULL OR v.duration_sec = 0 OR w.position_sec < v.duration_sec - $3)
		 ORDER BY w.updated_at DESC, v.id DESC
		 LIMIT $4`,
		userID, entity.MinResumeSec, entity.EndMarginSec, limit)
	if err != nil {
		return nil, fmt.Errorf("Repo - ContinueWatching: %w", err)
	}
	defer rows.Close()

	out := []entity.ContinueItem{}

	for rows.Next() {
		var it entity.ContinueItem

		v := &it.VOD
		if err = rows.Scan(&v.ID, &v.StreamID, &v.ChannelID, &v.Title, &v.DurationSec, &v.PlaylistPath, &v.ThumbnailURL,
			&v.Status, &v.CreatedAt, &v.ExpiresAt, &it.PositionSec, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("Repo - ContinueWatching - scan: %w", err)
		}

		out = append(out, it)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - ContinueWatching - rows: %w", err)
	}

	return out, nil
}

// PurgePlaybackSessions -.
func (r *Repo) PurgePlaybackSessions(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `DELETE FROM playback_sessions WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("Repo - PurgePlaybackSessions: %w", err)
	}

	return tag.RowsAffected(), nil
}
