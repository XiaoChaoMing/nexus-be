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

// _vodCols is shared by every query that returns entity.VOD; the names are unqualified so it also fits RETURNING
// and joins with watch_progress (no column in common).
const _vodCols = `id::text, stream_id::text, channel_id::text, source, title, description, COALESCE(duration_sec, 0),
	COALESCE(playlist_path, ''), COALESCE(thumbnail_url, ''), status, visibility, category_id::text, is_mature, created_at,
	published_at, expires_at, failure_reason, failure_retryable, processing_percent, processing_stage`

// vodDest lists the scan targets of _vodCols.
func vodDest(v *entity.VOD) []any {
	return []any{
		&v.ID, &v.StreamID, &v.ChannelID, &v.Source, &v.Title, &v.Description, &v.DurationSec, &v.PlaylistPath,
		&v.ThumbnailURL, &v.Status, &v.Visibility, &v.CategoryID, &v.IsMature, &v.CreatedAt, &v.PublishedAt, &v.ExpiresAt,
		&v.FailureReason, &v.FailureRetryable, &v.ProcessingPercent, &v.ProcessingStage,
	}
}

func scanVOD(row scanner) (entity.VOD, error) {
	var v entity.VOD

	err := row.Scan(vodDest(&v)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.VOD{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.VOD{}, fmt.Errorf("scan vod: %w", err)
	}

	return v, nil
}

// _unexpired: a broadcast VOD past its retention is gone; an uploaded video (expires_at NULL) never expires.
const _unexpired = `(expires_at IS NULL OR expires_at > now())`

// _publicVOD is what any viewer may see (BR-G2-07).
const _publicVOD = `status = 'READY' AND visibility = 'PUBLIC' AND ` + _unexpired

// vodAggregate is the outbox aggregate of a VOD event: the stream of a recording, the VOD itself for an upload.
func vodAggregate(v entity.VOD) string {
	if v.StreamID != nil {
		return *v.StreamID
	}

	return v.ID
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func utcOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	u := t.UTC()

	return &u
}

func vodDeletedEvent(v entity.VOD) events.VODDeleted {
	return events.VODDeleted{
		VODID: v.ID, StreamID: deref(v.StreamID), ChannelID: v.ChannelID, Source: v.Source, PlaylistPrefix: v.PlaylistPrefix(),
	}
}

func (r *Repo) vodUpdatedEvent(v entity.VOD) events.VODUpdated {
	return events.VODUpdated{
		VODID: v.ID, StreamID: deref(v.StreamID), ChannelID: v.ChannelID, Source: v.Source, Title: v.Title,
		Description: v.Description, Visibility: v.Visibility, Status: v.Status, ThumbnailURL: r.objectURL(v.ThumbnailURL),
		DurationSec: v.DurationSec, CategoryID: v.CategoryID, IsMature: v.IsMature, ExpiresAt: utcOrNil(v.ExpiresAt),
	}
}

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
		w.add("status IN ('READY', 'EXPIRED', 'PROCESSING', 'FAILED')")
	}

	if f.Visibility != nil {
		w.add("visibility = ?", *f.Visibility)
	}

	if f.Status != nil {
		w.add("status = ?", *f.Status)
	}

	if f.Source != nil {
		w.add("source = ?", *f.Source)
	}

	return r.vods(ctx, "ListByChannel", w, after, limit)
}

// UpdateVOD -.
func (r *Repo) UpdateVOD(ctx context.Context, channelID, vodID string, p entity.VODPatch) (entity.VOD, error) {
	var v entity.VOD

	err := r.inTx(ctx, "Repo - UpdateVOD", func(tx pgx.Tx) error {
		cur, err := scanVOD(tx.QueryRow(ctx,
			`SELECT `+_vodCols+` FROM vods WHERE id = $1 AND channel_id = $2 AND status <> 'DELETED' FOR UPDATE`,
			vodID, channelID))
		if errors.Is(err, entity.ErrNotFound) {
			return err
		}

		if err != nil {
			return fmt.Errorf("Repo - UpdateVOD - select: %w", err)
		}

		if aerr := cur.CanApply(p); aerr != nil {
			return aerr
		}

		v, err = scanVOD(tx.QueryRow(ctx,
			`UPDATE vods SET title = COALESCE($2, title), visibility = COALESCE($3, visibility),
			        description = COALESCE($4, description),
			        category_id = CASE WHEN $5::text IS NULL THEN category_id ELSE NULLIF($5::text, '')::uuid END,
			        is_mature = COALESCE($6, is_mature), updated_at = now()
			 WHERE id = $1 RETURNING `+_vodCols, vodID, p.Title, p.Visibility, p.Description, p.CategoryID, p.IsMature))
		if err != nil {
			return fmt.Errorf("Repo - UpdateVOD - update: %w", err)
		}

		// Other services learn about an uploaded video when it is published; until then there is nothing to update.
		if v.Status != entity.VODReady {
			return nil
		}

		return addOutbox(ctx, tx, vodAggregate(v), events.TypeVODUpdated, r.vodUpdatedEvent(v))
	})

	return v, err
}

// DeleteVOD -.
func (r *Repo) DeleteVOD(ctx context.Context, channelID, vodID string) error {
	return r.inTx(ctx, "Repo - DeleteVOD", func(tx pgx.Tx) error {
		v, err := scanVOD(tx.QueryRow(ctx,
			`UPDATE vods SET status = 'DELETED', deleted_at = now(), updated_at = now()
			 WHERE id = $1 AND channel_id = $2 AND status IN ('READY', 'EXPIRED', 'PROCESSING', 'FAILED')
			 RETURNING `+_vodCols, vodID, channelID))

		switch {
		case errors.Is(err, entity.ErrNotFound):
			return err
		case err != nil:
			return fmt.Errorf("Repo - DeleteVOD: %w", err)
		}

		return addOutbox(ctx, tx, vodAggregate(v), events.TypeVODDeleted, vodDeletedEvent(v))
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
		    (SELECT count(*) FROM vods WHERE channel_id = $1 AND status = 'READY' AND visibility = 'HIDDEN' AND `+_unexpired+`)`,
		channelID, since).Scan(&st.StreamCount, &st.LiveSeconds, &st.VODReadyPublic, &st.VODReadyHidden)
	if err != nil {
		return entity.DashboardStats{}, fmt.Errorf("Repo - DashboardStats: %w", err)
	}

	return st, nil
}

// RecentEnded lists broadcasts: uploaded videos have no stream and never join.
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
