package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

var (
	_ repo.SafetyRepo   = (*Repo)(nil)
	_ repo.SnapshotRepo = (*Repo)(nil)
)

// SetStatus -.
func (r *Repo) SetStatus(ctx context.Context, eventID string, c entity.StatusChange) (bool, error) {
	changed := false

	err := r.inEventTx(ctx, "Repo - SetStatus", eventID, func(tx pgx.Tx) error {
		var (
			ownerID string
			err     error
		)

		if c.Suspend {
			err = tx.QueryRow(ctx,
				`UPDATE channels SET status = 'SUSPENDED', suspended_at = now(), suspend_reason = $2, updated_at = now()
				 WHERE id = $1 AND status = 'ACTIVE' RETURNING owner_id::text`, c.ChannelID, c.Reason).Scan(&ownerID)
		} else {
			err = tx.QueryRow(ctx,
				`UPDATE channels SET status = 'ACTIVE', suspended_at = NULL, suspend_reason = NULL, updated_at = now()
				 WHERE id = $1 AND status = 'SUSPENDED' RETURNING owner_id::text`, c.ChannelID).Scan(&ownerID)
		}

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("Repo - SetStatus - update: %w", err)
		}

		changed = true

		return statusChanged(ctx, tx, c, ownerID)
	})

	return changed, err
}

// statusChanged hides (or shows again) the channel in search and announces the change. A restore puts back
// the channel's own document only: its stream and VOD documents come back with their next events.
func statusChanged(ctx context.Context, tx pgx.Tx, c entity.StatusChange, ownerID string) error {
	eventType := events.TypeChannelRestored

	if c.Suspend {
		eventType = events.TypeChannelSuspended

		if _, err := tx.Exec(ctx, `DELETE FROM search_documents WHERE channel_id = $1 OR (entity_type = 'CHANNEL' AND entity_id = $1)`,
			c.ChannelID); err != nil {
			return fmt.Errorf("Repo - SetStatus - search: %w", err)
		}
	} else if err := upsertChannelDoc(ctx, tx, c.ChannelID); err != nil {
		return err
	}

	actionID := c.ActionID

	return addOutbox(ctx, tx, "channel", c.ChannelID, eventType, events.ChannelSuspended{
		ChannelID: c.ChannelID, OwnerID: ownerID, Reason: c.Reason, ActionID: &actionID,
	})
}

// Channels -.
func (r *Repo) Channels(ctx context.Context, after string, limit int) ([]entity.ChannelCopy, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT id::text, owner_id::text, handle::text, display_name, avatar_url, status, suspended_at, updated_at
		 FROM channels WHERE ($1 = '' OR id > NULLIF($1, '')::uuid) ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("Repo - Channels: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.ChannelCopy, error) {
		var (
			c      entity.ChannelCopy
			status string
		)

		err := row.Scan(&c.ID, &c.OwnerID, &c.Handle, &c.DisplayName, &c.AvatarURL, &status, &c.SuspendedAt, &c.UpdatedAt)
		c.Status = entity.Status(status)

		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("Repo - Channels: %w", err)
	}

	return out, nil
}

// Follows -.
func (r *Repo) Follows(ctx context.Context, after *entity.FollowCopy, limit int) ([]entity.FollowCopy, error) {
	q := `SELECT user_id::text, channel_id::text, notify_level, created_at FROM follows`
	args := []any{limit}

	if after != nil {
		q += ` WHERE (user_id, channel_id) > ($2::uuid, $3::uuid)`

		args = append(args, after.UserID, after.ChannelID)
	}

	rows, err := r.Pool.Query(ctx, q+` ORDER BY user_id, channel_id LIMIT $1`, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - Follows: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.FollowCopy, error) {
		var f entity.FollowCopy

		err := row.Scan(&f.UserID, &f.ChannelID, &f.NotifyLevel, &f.CreatedAt)

		return f, err
	})
	if err != nil {
		return nil, fmt.Errorf("Repo - Follows: %w", err)
	}

	return out, nil
}
