package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
)

var _ repo.SafetyRepo = (*Repo)(nil)

// Takedown -.
func (r *Repo) Takedown(ctx context.Context, eventID, streamID string) (entity.TakedownResult, error) {
	var res entity.TakedownResult

	err := r.inEventTx(ctx, "Repo - Takedown", eventID, func(tx pgx.Tx) error {
		ended, err := endTx(ctx, tx, entity.EndAdminTakedown, "id = $2", streamID)
		if err != nil {
			return err
		}

		res.Ended = len(ended) > 0

		var channelID string

		err = tx.QueryRow(ctx,
			`UPDATE streams SET taken_down_at = COALESCE(taken_down_at, now()) WHERE id = $1 AND status = 'ENDED'
			 RETURNING channel_id::text`, streamID).Scan(&channelID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // unknown, SCHEDULED or CANCELLED: nothing to take down
		}

		if err != nil {
			return fmt.Errorf("Repo - Takedown - mark: %w", err)
		}

		res.Found = true
		res.VODsDeleted, err = deleteVODsOf(ctx, tx, streamID)

		return err
	})

	return res, err
}

// deleteVODsOf deletes the stream's VOD whatever its state and queues vod.deleted (media-svc removes the files).
func deleteVODsOf(ctx context.Context, tx pgx.Tx, streamID string) (int, error) {
	rows, err := tx.Query(ctx,
		`UPDATE vods SET status = 'DELETED', deleted_at = now(), updated_at = now()
		 WHERE stream_id = $1 AND status IN ('READY', 'EXPIRED', 'PROCESSING')
		 RETURNING id::text, channel_id::text`, streamID)
	if err != nil {
		return 0, fmt.Errorf("Repo - delete vods: %w", err)
	}

	deleted, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (events.VODDeleted, error) {
		e := events.VODDeleted{StreamID: streamID}
		err := row.Scan(&e.VODID, &e.ChannelID)

		return e, err
	})
	if err != nil {
		return 0, fmt.Errorf("Repo - delete vods - scan: %w", err)
	}

	for _, e := range deleted {
		if err := addOutbox(ctx, tx, streamID, events.TypeVODDeleted, e); err != nil {
			return 0, err
		}
	}

	return len(deleted), nil
}

// SetSuspended -.
func (r *Repo) SetSuspended(ctx context.Context, eventID, channelID string, suspended bool) (ended, cancelled int, err error) {
	err = r.inEventTx(ctx, "Repo - SetSuspended", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE creator_channels SET suspended = $2, updated_at = now() WHERE channel_id = $1`, channelID, suspended); err != nil {
			return fmt.Errorf("Repo - SetSuspended: %w", err)
		}

		if !suspended {
			return nil
		}

		list, err := endTx(ctx, tx, entity.EndAdminTakedown, "channel_id = $2", channelID)
		if err != nil {
			return err
		}

		ended = len(list)
		cancelled, err = cancelScheduled(ctx, tx, "channel_id = $1", channelID)

		return err
	})

	return ended, cancelled, err
}

// Streams -.
func (r *Repo) Streams(ctx context.Context, after string, limit int) ([]entity.StreamCopy, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT s.id::text, s.channel_id::text, s.title, s.status, s.end_reason, v.id::text, COALESCE(v.status = 'DELETED', false),
		        s.started_at, s.ended_at
		 FROM streams s LEFT JOIN vods v ON v.stream_id = s.id
		 WHERE ($1 = '' OR s.id > NULLIF($1, '')::uuid) ORDER BY s.id LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("Repo - Streams: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.StreamCopy, error) {
		var (
			c      entity.StreamCopy
			status string
		)

		err := row.Scan(&c.ID, &c.ChannelID, &c.Title, &status, &c.EndReason, &c.VODID, &c.VODDeleted, &c.StartedAt, &c.EndedAt)
		c.Status = entity.Status(status)

		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("Repo - Streams: %w", err)
	}

	return out, nil
}
