// Package persistent implements stream-svc storage on PostgreSQL (database nxl_stream).
package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
)

const (
	_uniqueViolation = "23505"
	_onAirIndex      = "streams_one_on_air_idx"
	_consumer        = "stream-svc"
)

// _streamCols is shared by every query that returns entity.Stream (alias s, LEFT JOIN vods v).
const _streamCols = `s.id::text, s.channel_id::text, s.title, s.tags, s.language, s.is_mature, s.status,
	s.started_at, s.interrupted_at, s.ended_at, s.end_reason, s.created_at, v.id::text`

const _streamFrom = ` FROM streams s LEFT JOIN vods v ON v.stream_id = s.id AND v.status = 'READY' `

// Repo implements repo.KeyRepo, repo.SettingsRepo, repo.StreamRepo and repo.ChannelRepo.
type Repo struct {
	*postgres.Postgres
}

// New -.
func New(pg *postgres.Postgres) *Repo {
	return &Repo{pg}
}

var (
	_ repo.KeyRepo      = (*Repo)(nil)
	_ repo.SettingsRepo = (*Repo)(nil)
	_ repo.StreamRepo   = (*Repo)(nil)
	_ repo.ChannelRepo  = (*Repo)(nil)
)

type scanner interface {
	Scan(dest ...any) error
}

func scanStream(row scanner) (entity.Stream, error) {
	var (
		s      entity.Stream
		status string
		reason *string
	)

	err := row.Scan(&s.ID, &s.ChannelID, &s.Title, &s.Tags, &s.Language, &s.IsMature, &status,
		&s.StartedAt, &s.InterruptedAt, &s.EndedAt, &reason, &s.CreatedAt, &s.VODID)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Stream{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Stream{}, fmt.Errorf("scan stream: %w", err)
	}

	s.Status = entity.Status(status)

	if reason != nil {
		r := entity.EndReason(*reason)
		s.EndReason = &r
	}

	if s.Tags == nil {
		s.Tags = []string{}
	}

	return s, nil
}

func (r *Repo) inTx(ctx context.Context, op string, fn func(tx pgx.Tx) error) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s - begin: %w", op, err)
	}

	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s - commit: %w", op, err)
	}

	return nil
}

// inEventTx runs fn once per event id: a redelivered event finds its processed_events row and is skipped.
func (r *Repo) inEventTx(ctx context.Context, op, eventID string, fn func(tx pgx.Tx) error) error {
	return r.inTx(ctx, op, func(tx pgx.Tx) error {
		if eventID != "" {
			tag, err := tx.Exec(ctx,
				`INSERT INTO processed_events (event_id, consumer) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				eventID, _consumer)
			if err != nil {
				return fmt.Errorf("%s - processed_events: %w", op, err)
			}

			if tag.RowsAffected() == 0 {
				return nil // duplicate delivery
			}
		}

		return fn(tx)
	})
}

func addOutbox(ctx context.Context, tx pgx.Tx, streamID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox marshal: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES ('stream', $1, $2, $3)`,
		streamID, eventType, b)
	if err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}

	return nil
}

func endedEvent(s entity.Stream) events.StreamEnded {
	reason := ""
	if s.EndReason != nil {
		reason = string(*s.EndReason)
	}

	return events.StreamEnded{StreamID: s.ID, ChannelID: s.ChannelID, EndReason: reason}
}

// ---- keys ----------------------------------------------------------------------------------------

// ChannelByKeyHash -.
func (r *Repo) ChannelByKeyHash(ctx context.Context, keyHash string) (string, error) {
	var channelID string

	err := r.Pool.QueryRow(ctx,
		`SELECT channel_id::text FROM stream_keys WHERE key_hash = $1 AND revoked_at IS NULL`, keyHash).Scan(&channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entity.ErrInvalidStreamKey
	}

	if err != nil {
		return "", fmt.Errorf("Repo - ChannelByKeyHash: %w", err)
	}

	return channelID, nil
}

// Replace -.
func (r *Repo) Replace(ctx context.Context, channelID, keyHash string) (time.Time, error) {
	var createdAt time.Time

	err := r.inTx(ctx, "Repo - Replace", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE stream_keys SET revoked_at = now() WHERE channel_id = $1 AND revoked_at IS NULL`, channelID); err != nil {
			return fmt.Errorf("Repo - Replace - revoke: %w", err)
		}

		if err := tx.QueryRow(ctx,
			`INSERT INTO stream_keys (channel_id, key_hash) VALUES ($1, $2) RETURNING created_at`,
			channelID, keyHash).Scan(&createdAt); err != nil {
			return fmt.Errorf("Repo - Replace - insert: %w", err)
		}

		return nil
	})

	return createdAt, err
}

// Info -.
func (r *Repo) Info(ctx context.Context, channelID string) (entity.StreamKeyInfo, error) {
	var createdAt time.Time

	err := r.Pool.QueryRow(ctx,
		`SELECT created_at FROM stream_keys WHERE channel_id = $1 AND revoked_at IS NULL`, channelID).Scan(&createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.StreamKeyInfo{}, nil
	}

	if err != nil {
		return entity.StreamKeyInfo{}, fmt.Errorf("Repo - Info: %w", err)
	}

	return entity.StreamKeyInfo{HasKey: true, CreatedAt: &createdAt}, nil
}

// ---- settings ------------------------------------------------------------------------------------

// GetSettings -.
func (r *Repo) GetSettings(ctx context.Context, channelID string) (entity.Settings, error) {
	return getSettings(ctx, r.Pool, channelID)
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getSettings(ctx context.Context, q querier, channelID string) (entity.Settings, error) {
	s := entity.Settings{ChannelID: channelID, Tags: []string{}, Language: "vi"}

	err := q.QueryRow(ctx,
		`SELECT title, tags, language, is_mature, updated_at FROM stream_settings WHERE channel_id = $1`,
		channelID).Scan(&s.Title, &s.Tags, &s.Language, &s.IsMature, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}

	if err != nil {
		return entity.Settings{}, fmt.Errorf("Repo - getSettings: %w", err)
	}

	return s, nil
}

// SaveSettings -.
func (r *Repo) SaveSettings(ctx context.Context, s entity.Settings) error {
	return r.inTx(ctx, "Repo - SaveSettings", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO stream_settings (channel_id, title, tags, language, is_mature, updated_at)
			 VALUES ($1, $2, $3, $4, $5, now())
			 ON CONFLICT (channel_id) DO UPDATE
			 SET title = EXCLUDED.title, tags = EXCLUDED.tags, language = EXCLUDED.language,
			     is_mature = EXCLUDED.is_mature, updated_at = now()`,
			s.ChannelID, s.Title, s.Tags, s.Language, s.IsMature); err != nil {
			return fmt.Errorf("Repo - Save - upsert: %w", err)
		}

		title := s.Title
		if title == "" {
			title = entity.DefaultTitle
		}

		if _, err := tx.Exec(ctx,
			`UPDATE streams SET title = $2, tags = $3, language = $4, is_mature = $5
			 WHERE channel_id = $1 AND status IN ('LIVE', 'INTERRUPTED')`,
			s.ChannelID, title, s.Tags, s.Language, s.IsMature); err != nil {
			return fmt.Errorf("Repo - Save - on-air stream: %w", err)
		}

		return nil
	})
}

// ---- state machine -------------------------------------------------------------------------------

// Authorize -.
func (r *Repo) Authorize(ctx context.Context, channelID string, grace time.Duration) (entity.Grant, error) {
	var g entity.Grant

	err := r.inTx(ctx, "Repo - Authorize", func(tx pgx.Tx) error {
		resumed, ok, err := onAirGrant(ctx, tx, channelID, grace)
		if err != nil {
			return err
		}

		if ok {
			g = resumed

			return nil
		}

		set, err := getSettings(ctx, tx, channelID)
		if err != nil {
			return err
		}

		title := set.Title
		if title == "" {
			title = entity.DefaultTitle
		}

		var id string

		err = tx.QueryRow(ctx,
			`INSERT INTO streams (channel_id, title, tags, language, is_mature, status, started_at)
			 VALUES ($1, $2, $3, $4, $5, 'LIVE', now()) RETURNING id::text`,
			channelID, title, set.Tags, set.Language, set.IsMature).Scan(&id)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == _uniqueViolation && pgErr.ConstraintName == _onAirIndex {
				return entity.ErrAlreadyLive
			}

			return fmt.Errorf("Repo - Authorize - insert: %w", err)
		}

		g = entity.Grant{StreamID: id, ChannelID: channelID}

		return addOutbox(ctx, tx, id, events.TypeStreamStarted,
			events.StreamStarted{StreamID: id, ChannelID: channelID, Title: title})
	})

	return g, err
}

// onAirGrant looks at the channel's on-air stream inside the Authorize transaction: it is resumed
// (INTERRUPTED inside grace, ok = true), refused (LIVE), or ended (INTERRUPTED past grace, not yet
// reached by the expiry job) so that a new stream can start.
func onAirGrant(ctx context.Context, tx pgx.Tx, channelID string, grace time.Duration) (entity.Grant, bool, error) {
	var (
		id, status string
		fresh      bool
	)

	err := tx.QueryRow(ctx,
		`SELECT id::text, status, COALESCE(interrupted_at > now() - make_interval(secs => $2), false)
		 FROM streams WHERE channel_id = $1 AND status IN ('LIVE', 'INTERRUPTED')
		 FOR UPDATE`,
		channelID, grace.Seconds()).Scan(&id, &status, &fresh)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return entity.Grant{}, false, nil
	case err != nil:
		return entity.Grant{}, false, fmt.Errorf("Repo - Authorize - select: %w", err)
	case status == string(entity.StatusLive):
		return entity.Grant{}, false, entity.ErrAlreadyLive
	case fresh:
		return entity.Grant{StreamID: id, ChannelID: channelID, Resumed: true}, true, nil
	}

	if _, err = endTx(ctx, tx, entity.EndIngestTimeout, "id = $2", id); err != nil {
		return entity.Grant{}, false, err
	}

	return entity.Grant{}, false, nil
}

// endTx ends the on-air streams matching where (its placeholders start at $2) and queues
// stream.ended for each.
func endTx(ctx context.Context, tx pgx.Tx, reason entity.EndReason, where string, args ...any) ([]entity.Stream, error) {
	rows, err := tx.Query(ctx,
		`WITH ended AS (
			UPDATE streams SET status = 'ENDED', ended_at = now(), end_reason = $1
			WHERE `+where+` AND status IN ('LIVE', 'INTERRUPTED')
			RETURNING *
		 )
		 SELECT `+_streamCols+` FROM ended s LEFT JOIN vods v ON v.stream_id = s.id AND v.status = 'READY'`,
		append([]any{string(reason)}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("end streams: %w", err)
	}

	var out []entity.Stream

	for rows.Next() {
		s, serr := scanStream(rows)
		if serr != nil {
			rows.Close()

			return nil, serr
		}

		out = append(out, s)
	}

	rows.Close()

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("end streams: %w", err)
	}

	for _, s := range out {
		if err := addOutbox(ctx, tx, s.ID, events.TypeStreamEnded, endedEvent(s)); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// End -.
func (r *Repo) End(ctx context.Context, channelID string, reason entity.EndReason) (entity.Stream, error) {
	var s entity.Stream

	err := r.inTx(ctx, "Repo - End", func(tx pgx.Tx) error {
		ended, err := endTx(ctx, tx, reason, "channel_id = $2", channelID)
		if err != nil {
			return err
		}

		if len(ended) == 0 {
			return entity.ErrNotLive
		}

		s = ended[0]

		return nil
	})

	return s, err
}

// ExpireInterrupted -.
func (r *Repo) ExpireInterrupted(ctx context.Context, grace time.Duration) ([]entity.Stream, error) {
	var out []entity.Stream

	err := r.inTx(ctx, "Repo - ExpireInterrupted", func(tx pgx.Tx) error {
		var err error

		out, err = endTx(ctx, tx, entity.EndIngestTimeout,
			"status = 'INTERRUPTED' AND interrupted_at <= now() - make_interval(secs => $2)", grace.Seconds())

		return err
	})

	return out, err
}

// OnIngestStarted -.
func (r *Repo) OnIngestStarted(ctx context.Context, eventID, streamID string) error {
	return r.inEventTx(ctx, "Repo - OnIngestStarted", eventID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE streams SET status = 'LIVE', interrupted_at = NULL WHERE id = $1 AND status = 'INTERRUPTED'`, streamID)
		if err != nil {
			return fmt.Errorf("Repo - OnIngestStarted: %w", err)
		}

		return nil
	})
}

// OnIngestStopped -.
func (r *Repo) OnIngestStopped(ctx context.Context, eventID, streamID string) error {
	return r.inEventTx(ctx, "Repo - OnIngestStopped", eventID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE streams SET status = 'INTERRUPTED', interrupted_at = now() WHERE id = $1 AND status = 'LIVE'`, streamID)
		if err != nil {
			return fmt.Errorf("Repo - OnIngestStopped: %w", err)
		}

		return nil
	})
}

// OnVODProcessed ends the stream if media-svc finished it first (shutdown), then records the VOD.
func (r *Repo) OnVODProcessed(ctx context.Context, eventID string, v repo.VODInput) error {
	return r.inEventTx(ctx, "Repo - OnVODProcessed", eventID, func(tx pgx.Tx) error {
		if _, err := endTx(ctx, tx, entity.EndIngestTimeout, "id = $2", v.StreamID); err != nil {
			return err
		}

		_, err := tx.Exec(ctx,
			`INSERT INTO vods (stream_id, channel_id, title, duration_sec, playlist_path, thumbnail_url, status, expires_at)
			 SELECT id, channel_id, title, $2, $3, $4, 'READY', now() + make_interval(secs => $5)
			 FROM streams WHERE id = $1
			 ON CONFLICT (stream_id) DO NOTHING`,
			v.StreamID, v.DurationSec, v.PlaylistPath, v.ThumbnailURL, v.Retention.Seconds())
		if err != nil {
			return fmt.Errorf("Repo - OnVODProcessed - insert vod: %w", err)
		}

		return nil
	})
}

// ExpireVODs -.
func (r *Repo) ExpireVODs(ctx context.Context) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `UPDATE vods SET status = 'EXPIRED' WHERE status = 'READY' AND expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("Repo - ExpireVODs: %w", err)
	}

	return tag.RowsAffected(), nil
}

// ---- queries -------------------------------------------------------------------------------------

// EndedByCreatorSince -.
func (r *Repo) EndedByCreatorSince(ctx context.Context, channelID string, since time.Time) (bool, error) {
	var recent bool

	err := r.Pool.QueryRow(ctx,
		`SELECT COALESCE(
			(SELECT end_reason = 'creator' AND ended_at > $2
			 FROM streams WHERE channel_id = $1 AND status = 'ENDED'
			 ORDER BY ended_at DESC LIMIT 1), false)`,
		channelID, since).Scan(&recent)
	if err != nil {
		return false, fmt.Errorf("Repo - EndedByCreatorSince: %w", err)
	}

	return recent, nil
}

// CountOnAir -.
func (r *Repo) CountOnAir(ctx context.Context) (int, error) {
	var n int

	if err := r.Pool.QueryRow(ctx, `SELECT count(*) FROM streams WHERE status IN ('LIVE', 'INTERRUPTED')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("Repo - CountOnAir: %w", err)
	}

	return n, nil
}

// Get -.
func (r *Repo) Get(ctx context.Context, streamID string) (entity.Stream, error) {
	return scanStream(r.Pool.QueryRow(ctx, `SELECT `+_streamCols+_streamFrom+`WHERE s.id = $1`, streamID))
}

// Current -.
func (r *Repo) Current(ctx context.Context, channelID string) (*entity.Stream, error) {
	s, err := scanStream(r.Pool.QueryRow(ctx,
		`SELECT `+_streamCols+_streamFrom+`WHERE s.channel_id = $1 AND s.status IN ('LIVE', 'INTERRUPTED')`, channelID))
	if errors.Is(err, entity.ErrNotFound) {
		return nil, nil //nolint:nilnil // no on-air stream is not an error
	}

	if err != nil {
		return nil, err
	}

	return &s, nil
}

// ListOnAir -.
func (r *Repo) ListOnAir(ctx context.Context, after *repo.Cursor, limit int) ([]entity.Stream, error) {
	q := `SELECT ` + _streamCols + _streamFrom + `WHERE s.status IN ('LIVE', 'INTERRUPTED')`
	args := []any{limit}

	if after != nil {
		q += ` AND (s.started_at, s.id) < ($2, $3::uuid)`

		args = append(args, after.StartedAt, after.ID)
	}

	q += ` ORDER BY s.started_at DESC, s.id DESC LIMIT $1`

	rows, err := r.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - ListOnAir: %w", err)
	}
	defer rows.Close()

	out := []entity.Stream{}

	for rows.Next() {
		s, serr := scanStream(rows)
		if serr != nil {
			return nil, serr
		}

		out = append(out, s)
	}

	return out, rows.Err()
}

// GetVOD returns READY, unexpired VODs only.
func (r *Repo) GetVOD(ctx context.Context, vodID string) (entity.VOD, error) {
	var (
		v        entity.VOD
		duration *int
		path     *string
		thumb    *string
	)

	err := r.Pool.QueryRow(ctx,
		`SELECT id::text, stream_id::text, channel_id::text, title, duration_sec, playlist_path, thumbnail_url,
		        status, created_at, expires_at
		 FROM vods WHERE id = $1 AND status = 'READY' AND expires_at > now()`, vodID).
		Scan(&v.ID, &v.StreamID, &v.ChannelID, &v.Title, &duration, &path, &thumb, &v.Status, &v.CreatedAt, &v.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.VOD{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.VOD{}, fmt.Errorf("Repo - GetVOD: %w", err)
	}

	if duration != nil {
		v.DurationSec = *duration
	}

	if path != nil {
		v.PlaylistPath = *path
	}

	if thumb != nil {
		v.ThumbnailURL = *thumb
	}

	return v, nil
}

// ---- creator channels ----------------------------------------------------------------------------

const _channelCols = `channel_id::text, owner_id::text, handle, display_name, avatar_url`

func scanChannel(row scanner) (entity.Channel, error) {
	var c entity.Channel

	err := row.Scan(&c.ID, &c.OwnerID, &c.Handle, &c.DisplayName, &c.AvatarURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Channel{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("scan channel: %w", err)
	}

	return c, nil
}

// ChannelOfOwner -.
func (r *Repo) ChannelOfOwner(ctx context.Context, ownerID string) (entity.Channel, error) {
	c, err := scanChannel(r.Pool.QueryRow(ctx,
		`SELECT `+_channelCols+` FROM creator_channels WHERE owner_id = $1`, ownerID))
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return entity.Channel{}, fmt.Errorf("Repo - ChannelOfOwner: %w", err)
	}

	return c, err
}

// ChannelsByID -.
func (r *Repo) ChannelsByID(ctx context.Context, ids []string) (map[string]entity.Channel, error) {
	out := make(map[string]entity.Channel, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx,
		`SELECT `+_channelCols+` FROM creator_channels WHERE channel_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("Repo - ChannelsByID: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		c, serr := scanChannel(rows)
		if serr != nil {
			return nil, fmt.Errorf("Repo - ChannelsByID: %w", serr)
		}

		out[c.ID] = c
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - ChannelsByID: %w", err)
	}

	return out, nil
}

// UpsertChannel -.
func (r *Repo) UpsertChannel(ctx context.Context, eventID string, c entity.Channel) error {
	return r.inEventTx(ctx, "Repo - UpsertChannel", eventID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO creator_channels (owner_id, channel_id, handle, display_name, avatar_url, updated_at)
			 VALUES ($1, $2, $3, $4, $5, now())
			 ON CONFLICT (owner_id) DO UPDATE
			 SET channel_id = EXCLUDED.channel_id, handle = EXCLUDED.handle, display_name = EXCLUDED.display_name,
			     avatar_url = EXCLUDED.avatar_url, updated_at = now()`,
			c.OwnerID, c.ID, c.Handle, c.DisplayName, c.AvatarURL)
		if err != nil {
			return fmt.Errorf("Repo - UpsertChannel: %w", err)
		}

		return nil
	})
}
