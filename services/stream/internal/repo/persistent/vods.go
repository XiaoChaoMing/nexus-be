package persistent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
)

var _ repo.VODRepo = (*Repo)(nil)

const _vodCols = `id::text, stream_id::text, channel_id::text, title, COALESCE(duration_sec, 0), COALESCE(playlist_path, ''),
	COALESCE(thumbnail_url, ''), status, visibility, category_id::text, is_mature, created_at, expires_at`

func scanVOD(row scanner) (entity.VOD, error) {
	var v entity.VOD

	err := row.Scan(&v.ID, &v.StreamID, &v.ChannelID, &v.Title, &v.DurationSec, &v.PlaylistPath, &v.ThumbnailURL, &v.Status,
		&v.Visibility, &v.CategoryID, &v.IsMature, &v.CreatedAt, &v.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.VOD{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.VOD{}, fmt.Errorf("scan vod: %w", err)
	}

	return v, nil
}

// _publicVOD is what any viewer may see (BR-G2-07).
const _publicVOD = `status = 'READY' AND visibility = 'PUBLIC' AND expires_at > now()`

// where builds a WHERE clause from conditions whose placeholders are numbered as args grows.
type where struct {
	conds []string
	args  []any
}

func (w *where) add(cond string, args ...any) {
	for _, a := range args {
		w.args = append(w.args, a)
		cond = strings.Replace(cond, "?", "$"+strconv.Itoa(len(w.args)), 1)
	}

	w.conds = append(w.conds, cond)
}

func (w *where) sql() string {
	out := ""

	for i, c := range w.conds {
		if i > 0 {
			out += " AND "
		}

		out += "(" + c + ")"
	}

	return out
}

func (r *Repo) vods(ctx context.Context, op string, w *where, after *repo.VODCursor, limit int) ([]entity.VOD, error) {
	if after != nil {
		w.add("(created_at, id) < (?, ?::uuid)", after.CreatedAt, after.ID)
	}

	w.args = append(w.args, limit)

	rows, err := r.Pool.Query(ctx,
		`SELECT `+_vodCols+` FROM vods WHERE `+w.sql()+` ORDER BY created_at DESC, id DESC LIMIT $`+strconv.Itoa(len(w.args)),
		w.args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}
	defer rows.Close()

	out := []entity.VOD{}

	for rows.Next() {
		v, serr := scanVOD(rows)
		if serr != nil {
			return nil, fmt.Errorf("Repo - %s: %w", op, serr)
		}

		out = append(out, v)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}

	return out, nil
}

// ListPublic -.
func (r *Repo) ListPublic(ctx context.Context, f repo.VODFilter, safeOnly bool, after *repo.VODCursor, limit int) ([]entity.VOD, error) {
	w := &where{}
	w.add(_publicVOD)

	if len(f.CategoryIDs) > 0 {
		w.add("category_id = ANY(?::uuid[])", f.CategoryIDs)
	}

	if safeOnly {
		w.add("NOT is_mature")
	}

	return r.vods(ctx, "ListPublic", w, after, limit)
}

// ListByChannel -.
func (r *Repo) ListByChannel(ctx context.Context, channelID string, public bool, f repo.StudioVODFilter, after *repo.VODCursor,
	limit int,
) ([]entity.VOD, error) {
	w := &where{}
	w.add("channel_id = ?", channelID)

	if public {
		w.add(_publicVOD)
	} else {
		w.add("status IN ('READY', 'EXPIRED')")
	}

	if f.Visibility != nil {
		w.add("visibility = ?", *f.Visibility)
	}

	if f.Status != nil {
		w.add("status = ?", *f.Status)
	}

	return r.vods(ctx, "ListByChannel", w, after, limit)
}

// UpdateVOD -.
func (r *Repo) UpdateVOD(ctx context.Context, channelID, vodID string, p entity.VODPatch) (entity.VOD, error) {
	var v entity.VOD

	err := r.inTx(ctx, "Repo - UpdateVOD", func(tx pgx.Tx) error {
		var status string

		err := tx.QueryRow(ctx,
			`SELECT status FROM vods WHERE id = $1 AND channel_id = $2 AND status IN ('READY', 'EXPIRED') FOR UPDATE`,
			vodID, channelID).Scan(&status)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return entity.ErrNotFound
		case err != nil:
			return fmt.Errorf("Repo - UpdateVOD - select: %w", err)
		case status != entity.VODReady:
			return entity.ErrInvalidState
		}

		v, err = scanVOD(tx.QueryRow(ctx,
			`UPDATE vods SET title = COALESCE($2, title), visibility = COALESCE($3, visibility), updated_at = now()
			 WHERE id = $1 RETURNING `+_vodCols, vodID, p.Title, p.Visibility))
		if err != nil {
			return fmt.Errorf("Repo - UpdateVOD - update: %w", err)
		}

		return addOutbox(ctx, tx, v.StreamID, events.TypeVODUpdated, events.VODUpdated{
			VODID: v.ID, StreamID: v.StreamID, ChannelID: v.ChannelID, Title: v.Title, Visibility: v.Visibility,
			Status: v.Status, ThumbnailURL: r.objectURL(v.ThumbnailURL), DurationSec: v.DurationSec, CategoryID: v.CategoryID,
			IsMature: v.IsMature, ExpiresAt: v.ExpiresAt.UTC(),
		})
	})

	return v, err
}

// DeleteVOD -.
func (r *Repo) DeleteVOD(ctx context.Context, channelID, vodID string) error {
	return r.inTx(ctx, "Repo - DeleteVOD", func(tx pgx.Tx) error {
		var streamID string

		err := tx.QueryRow(ctx,
			`UPDATE vods SET status = 'DELETED', deleted_at = now(), updated_at = now()
			 WHERE id = $1 AND channel_id = $2 AND status IN ('READY', 'EXPIRED')
			 RETURNING stream_id::text`, vodID, channelID).Scan(&streamID)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return entity.ErrNotFound
		case err != nil:
			return fmt.Errorf("Repo - DeleteVOD: %w", err)
		}

		return addOutbox(ctx, tx, streamID, events.TypeVODDeleted, events.VODDeleted{VODID: vodID, StreamID: streamID, ChannelID: channelID})
	})
}

// DashboardStats -.
func (r *Repo) DashboardStats(ctx context.Context, channelID string, since time.Time) (entity.DashboardStats, error) {
	var st entity.DashboardStats

	err := r.Pool.QueryRow(ctx,
		`SELECT
		    (SELECT count(*) FROM streams WHERE channel_id = $1 AND started_at >= $2),
		    (SELECT COALESCE(sum(extract(epoch FROM COALESCE(ended_at, now()) - started_at)), 0)::bigint
		     FROM streams WHERE channel_id = $1 AND started_at >= $2),
		    (SELECT count(*) FROM vods WHERE channel_id = $1 AND `+_publicVOD+`),
		    (SELECT count(*) FROM vods WHERE channel_id = $1 AND status = 'READY' AND visibility = 'HIDDEN' AND expires_at > now())`,
		channelID, since).Scan(&st.StreamCount, &st.LiveSeconds, &st.VODReadyPublic, &st.VODReadyHidden)
	if err != nil {
		return entity.DashboardStats{}, fmt.Errorf("Repo - DashboardStats: %w", err)
	}

	return st, nil
}

// RecentEnded -.
func (r *Repo) RecentEnded(ctx context.Context, channelID string, n int) ([]entity.RecentStream, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT s.id::text, s.title, s.started_at, s.ended_at, s.end_reason, s.peak_viewers, v.id::text, v.visibility
		 FROM streams s LEFT JOIN vods v ON v.stream_id = s.id AND v.status = 'READY'
		 WHERE s.channel_id = $1 AND s.status = 'ENDED'
		 ORDER BY s.started_at DESC, s.id DESC LIMIT $2`, channelID, n)
	if err != nil {
		return nil, fmt.Errorf("Repo - RecentEnded: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.RecentStream, error) {
		var s entity.RecentStream

		return s, row.Scan(&s.StreamID, &s.Title, &s.StartedAt, &s.EndedAt, &s.EndReason, &s.PeakViewers, &s.VODID, &s.VODVisibility)
	})
	if err != nil {
		return nil, fmt.Errorf("Repo - RecentEnded - scan: %w", err)
	}

	return out, nil
}
