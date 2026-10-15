package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
)

const (
	_recipientCols = `u.id::text, u.username, u.display_name, u.email::text, u.email_verified, u.locale, u.timezone,
	(u.status = 'ACTIVE' OR (u.status = 'LOCKED' AND u.locked_until IS NOT NULL AND u.locked_until <= now()))`
	_channelCols = `id::text, owner_id::text, handle, display_name, avatar_url, status`
)

func scanRecipient(row scanner) (entity.Recipient, error) {
	var r entity.Recipient

	err := row.Scan(&r.UserID, &r.Username, &r.DisplayName, &r.Email, &r.EmailVerified, &r.Locale, &r.Timezone, &r.Active)

	return r, err //nolint:wrapcheck // callers wrap
}

// Channel -.
func (r *Repo) Channel(ctx context.Context, id string) (entity.ChannelCopy, error) {
	var c entity.ChannelCopy

	if uuid.Validate(id) != nil {
		return c, entity.ErrNotFound
	}

	err := r.Pool.QueryRow(ctx, `SELECT `+_channelCols+` FROM channels WHERE id = $1`, id).
		Scan(&c.ID, &c.OwnerID, &c.Handle, &c.DisplayName, &c.AvatarURL, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, entity.ErrNotFound
	}

	if err != nil {
		return c, fmt.Errorf("RecipientRepo - Channel: %w", err)
	}

	return c, nil
}

// Followers -.
func (r *Repo) Followers(ctx context.Context, channelID string, levels []string, excludeUserID, afterUserID string,
	limit int,
) ([]entity.Recipient, error) {
	if afterUserID == "" {
		afterUserID = uuid.Nil.String()
	}

	rows, err := r.Pool.Query(ctx, `SELECT `+_recipientCols+` FROM follows f JOIN users u ON u.id = f.user_id
		WHERE f.channel_id = $1 AND f.notify_level = ANY($2) AND f.user_id <> $3 AND f.user_id > $4
			AND (u.status = 'ACTIVE' OR (u.status = 'LOCKED' AND u.locked_until IS NOT NULL AND u.locked_until <= now()))
		ORDER BY f.user_id LIMIT $5`, channelID, levels, excludeUserID, afterUserID, limit)
	if err != nil {
		return nil, fmt.Errorf("RecipientRepo - Followers: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.Recipient, error) { return scanRecipient(row) })
	if err != nil {
		return nil, fmt.Errorf("RecipientRepo - Followers - scan: %w", err)
	}

	return out, nil
}

func validIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if uuid.Validate(id) == nil {
			out = append(out, id)
		}
	}

	return out
}

// Recipients -.
func (r *Repo) Recipients(ctx context.Context, userIDs []string) (map[string]entity.Recipient, error) {
	out := map[string]entity.Recipient{}

	ids := validIDs(userIDs)
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx, `SELECT `+_recipientCols+` FROM users u WHERE u.id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("RecipientRepo - Recipients: %w", err)
	}

	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.Recipient, error) { return scanRecipient(row) })
	if err != nil {
		return nil, fmt.Errorf("RecipientRepo - Recipients - scan: %w", err)
	}

	for _, rc := range list {
		out[rc.UserID] = rc
	}

	return out, nil
}

// BlockedBy -.
func (r *Repo) BlockedBy(ctx context.Context, senderID string, userIDs []string) (map[string]bool, error) {
	out := map[string]bool{}

	ids := validIDs(userIDs)
	if len(ids) == 0 || uuid.Validate(senderID) != nil {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx, `SELECT blocker_id::text FROM user_blocks
		WHERE blocked_id = $1 AND blocker_id = ANY($2::uuid[])`, senderID, ids)
	if err != nil {
		return nil, fmt.Errorf("RecipientRepo - BlockedBy: %w", err)
	}

	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("RecipientRepo - BlockedBy - scan: %w", err)
	}

	for _, id := range list {
		out[id] = true
	}

	return out, nil
}

// PatchUser inserts the copy when the patch has a username and an e-mail (user.registered, backfill),
// otherwise updates an existing one; a copy synced after at is left alone.
func (r *Repo) PatchUser(ctx context.Context, at time.Time, u entity.UserCopy) error {
	const set = `display_name = coalesce($3, users.display_name), email = coalesce($4, users.email),
		email_verified = coalesce($5, users.email_verified), locale = coalesce(nullif($6, ''), users.locale),
		timezone = coalesce(nullif($7, ''), users.timezone), status = coalesce($8, users.status),
		locked_until = CASE WHEN $8::text IS NULL THEN users.locked_until ELSE $9 END,
		synced_at = greatest(users.synced_at, $10)`

	sql := `UPDATE users SET ` + set + ` WHERE id = $1 AND synced_at <= $10 AND ($2::text IS NULL OR true)`
	if u.Username != nil && u.Email != nil {
		sql = `INSERT INTO users (id, username, display_name, email, email_verified, locale, timezone, status,
				locked_until, synced_at)
			VALUES ($1, $2, coalesce($3, ''), $4, coalesce($5, false), coalesce(nullif($6, ''), 'vi'),
				coalesce(nullif($7, ''), 'Asia/Ho_Chi_Minh'), coalesce($8, 'ACTIVE'), $9, $10)
			ON CONFLICT (id) DO UPDATE SET username = EXCLUDED.username, ` + set + ` WHERE users.synced_at <= $10`
	}

	_, err := r.Pool.Exec(ctx, sql, u.ID, u.Username, u.DisplayName, u.Email, u.EmailVerified, u.Locale, u.Timezone,
		u.Status, u.LockedUntil, at)
	if err != nil {
		return fmt.Errorf("ReplicaRepo - PatchUser: %w", err)
	}

	return nil
}

// PatchChannel inserts the copy when the patch has an owner and a handle, otherwise updates it.
func (r *Repo) PatchChannel(ctx context.Context, at time.Time, c entity.ChannelPatch) error {
	const set = `owner_id = coalesce($2, channels.owner_id), display_name = coalesce($4, channels.display_name),
		avatar_url = CASE WHEN $6 THEN $5 ELSE channels.avatar_url END, status = coalesce($7, channels.status),
		synced_at = greatest(channels.synced_at, $8)`

	sql := `UPDATE channels SET ` + set + ` WHERE id = $1 AND synced_at <= $8 AND ($3::text IS NULL OR true)`
	if c.Handle != nil && c.OwnerID != nil {
		sql = `INSERT INTO channels (id, owner_id, handle, display_name, avatar_url, status, synced_at)
			VALUES ($1, $2, $3, coalesce($4, ''), $5, coalesce($7, 'ACTIVE'), $8)
			ON CONFLICT (id) DO UPDATE SET handle = EXCLUDED.handle, ` + set + ` WHERE channels.synced_at <= $8`
	}

	_, err := r.Pool.Exec(ctx, sql, c.ID, c.OwnerID, c.Handle, c.DisplayName, c.AvatarURL, c.SetAvatar, c.Status, at)
	if err != nil {
		return fmt.Errorf("ReplicaRepo - PatchChannel: %w", err)
	}

	return nil
}

// SetFollow upserts a follow unless the copy was synced after at.
func (r *Repo) SetFollow(ctx context.Context, at time.Time, f entity.Follow) error {
	created := f.CreatedAt
	if created.IsZero() {
		created = at
	}

	_, err := r.Pool.Exec(ctx, `INSERT INTO follows (channel_id, user_id, notify_level, created_at, synced_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (channel_id, user_id) DO UPDATE SET notify_level = EXCLUDED.notify_level,
			synced_at = greatest(follows.synced_at, EXCLUDED.synced_at) WHERE follows.synced_at <= EXCLUDED.synced_at`,
		f.ChannelID, f.UserID, f.NotifyLevel, created, at)
	if err != nil {
		return fmt.Errorf("ReplicaRepo - SetFollow: %w", err)
	}

	return nil
}

// RemoveFollow deletes a follow unless it was synced after at.
func (r *Repo) RemoveFollow(ctx context.Context, at time.Time, userID, channelID string) error {
	_, err := r.Pool.Exec(ctx, `DELETE FROM follows WHERE channel_id = $1 AND user_id = $2 AND synced_at <= $3`,
		channelID, userID, at)
	if err != nil {
		return fmt.Errorf("ReplicaRepo - RemoveFollow: %w", err)
	}

	return nil
}

// SetBlock -.
func (r *Repo) SetBlock(ctx context.Context, blockerID, blockedID string, blocked bool) error {
	sql := `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	if !blocked {
		sql = `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`
	}

	if _, err := r.Pool.Exec(ctx, sql, blockerID, blockedID); err != nil {
		return fmt.Errorf("ReplicaRepo - SetBlock: %w", err)
	}

	return nil
}

// BackfillDone -.
func (r *Repo) BackfillDone(ctx context.Context, name string) (bool, error) {
	var ok bool

	err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM replica_backfills WHERE name = $1)`, name).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("ReplicaRepo - BackfillDone: %w", err)
	}

	return ok, nil
}

// MarkBackfilled -.
func (r *Repo) MarkBackfilled(ctx context.Context, name string) error {
	if _, err := r.Pool.Exec(ctx, `INSERT INTO replica_backfills (name) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
		return fmt.Errorf("ReplicaRepo - MarkBackfilled: %w", err)
	}

	return nil
}
