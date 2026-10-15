package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
)

// BlockRepo -.
type BlockRepo struct {
	*postgres.Postgres
}

// NewBlockRepo -.
func NewBlockRepo(pg *postgres.Postgres) repo.BlockRepo {
	return &BlockRepo{pg}
}

// List -.
func (r *BlockRepo) List(ctx context.Context, userID string, after *repo.BlockCursor, limit int) ([]entity.Block, error) {
	q := `SELECT b.blocked_id::text, u.username::text, u.display_name, b.created_at
	      FROM user_blocks b JOIN users u ON u.id = b.blocked_id
	      WHERE b.blocker_id = $1`
	args := []any{userID, limit}

	if after != nil {
		q += ` AND (b.created_at, b.blocked_id) < ($3, $4::uuid)`

		args = append(args, after.CreatedAt, after.UserID)
	}

	rows, err := r.Pool.Query(ctx, q+` ORDER BY b.created_at DESC, b.blocked_id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("BlockRepo - List: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.Block, error) {
		var b entity.Block

		return b, row.Scan(&b.UserID, &b.Username, &b.DisplayName, &b.CreatedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("BlockRepo - List: %w", err)
	}

	return out, nil
}

// IDs -.
func (r *BlockRepo) IDs(ctx context.Context, userID string, limit int) ([]string, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT blocked_id::text FROM user_blocks WHERE blocker_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("BlockRepo - IDs: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("BlockRepo - IDs: %w", err)
	}

	return ids, nil
}

// Add counts under a lock on the blocker's users row, so two concurrent blocks cannot pass the limit.
func (r *BlockRepo) Add(ctx context.Context, userID, targetID string, maxBlocks int) (bool, error) {
	added := false

	err := inTx(ctx, r.Pool, "BlockRepo - Add", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); err != nil {
			return fmt.Errorf("BlockRepo - Add - lock: %w", err)
		}

		var exists, already bool

		var count int

		err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $2 AND status <> 'DELETED'),
			        EXISTS (SELECT 1 FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2),
			        (SELECT count(*) FROM user_blocks WHERE blocker_id = $1)`, userID, targetID).Scan(&exists, &already, &count)
		if err != nil {
			return fmt.Errorf("BlockRepo - Add - check: %w", err)
		}

		switch {
		case !exists:
			return entity.ErrNotFound
		case already:
			return nil
		case count >= maxBlocks:
			return entity.ErrBlockLimit
		}

		if _, err = tx.Exec(ctx, `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, userID, targetID); err != nil {
			return fmt.Errorf("BlockRepo - Add - insert: %w", err)
		}

		added = true

		return addOutbox(ctx, tx, _aggregateUser, userID, events.TypeUserBlocked, events.UserBlocked{BlockerID: userID, BlockedID: targetID})
	})

	return added, err
}

// Remove -.
func (r *BlockRepo) Remove(ctx context.Context, userID, targetID string) (bool, error) {
	removed := false

	err := inTx(ctx, r.Pool, "BlockRepo - Remove", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`, userID, targetID)
		if err != nil {
			return fmt.Errorf("BlockRepo - Remove: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return nil
		}

		removed = true

		return addOutbox(ctx, tx, _aggregateUser, userID, events.TypeUserUnblocked, events.UserBlocked{BlockerID: userID, BlockedID: targetID})
	})

	return removed, err
}

// ModerationRepo -.
type ModerationRepo struct {
	*postgres.Postgres
}

// NewModerationRepo -.
func NewModerationRepo(pg *postgres.Postgres) *ModerationRepo {
	return &ModerationRepo{pg}
}

var (
	_ repo.ModerationRepo = (*ModerationRepo)(nil)
	_ repo.SnapshotRepo   = (*ModerationRepo)(nil)
)

// ApplyUserAction -.
func (r *ModerationRepo) ApplyUserAction(ctx context.Context, eventID string, e events.UserActioned) (bool, error) {
	changed := false

	err := inTx(ctx, r.Pool, "ModerationRepo - ApplyUserAction", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO processed_events (event_id, consumer) VALUES ($1, $2) ON CONFLICT DO NOTHING`, eventID, _consumer)
		if err != nil {
			return fmt.Errorf("ModerationRepo - processed_events: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return nil // duplicate delivery
		}

		if e.Action == events.ActionLock {
			changed, err = lockUser(ctx, tx, e)
		} else {
			changed, err = unlockUser(ctx, tx, e)
		}

		return err
	})

	return changed, err
}

// lockUser sets an ADMIN lock (it replaces any lock, a newer one moves locked_until) and deletes the user's
// sessions: deleted rather than revoked, so a refresh with an old token is a plain "session expired" and not
// reuse detection.
func lockUser(ctx context.Context, tx pgx.Tx, e events.UserActioned) (bool, error) {
	var id string

	err := tx.QueryRow(ctx,
		`UPDATE users SET status = 'LOCKED', locked_until = $2, lock_kind = 'ADMIN', lock_reason = $3, updated_at = now()
		 WHERE id = $1 AND global_role <> 'ADMIN' AND status <> 'DELETED' RETURNING id::text`,
		e.UserID, e.LockedUntil, e.Reason).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("ModerationRepo - lock: %w", err)
	}

	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, e.UserID); err != nil {
		return false, fmt.Errorf("ModerationRepo - lock - sessions: %w", err)
	}

	actionID := e.ActionID

	err = addOutbox(ctx, tx, _aggregateUser, e.UserID, events.TypeUserLocked, events.UserLocked{
		UserID: e.UserID, LockedUntil: e.LockedUntil, Reason: e.Reason, ActionID: &actionID,
	})
	if err != nil {
		return false, fmt.Errorf("ModerationRepo - lock - %w", err)
	}

	return true, nil
}

// unlockUser lifts any lock, a failed-login one included.
func unlockUser(ctx context.Context, tx pgx.Tx, e events.UserActioned) (bool, error) {
	tag, err := tx.Exec(ctx,
		`UPDATE users SET status = 'ACTIVE', locked_until = NULL, lock_kind = NULL, lock_reason = NULL, failed_logins = 0,
		        last_failed_login_at = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'LOCKED'`, e.UserID)
	if err != nil {
		return false, fmt.Errorf("ModerationRepo - unlock: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return false, nil
	}

	actionID := e.ActionID

	err = addOutbox(ctx, tx, _aggregateUser, e.UserID, events.TypeUserUnlocked, events.UserUnlocked{UserID: e.UserID, ActionID: &actionID})
	if err != nil {
		return false, fmt.Errorf("ModerationRepo - unlock - %w", err)
	}

	return true, nil
}

// Users -.
func (r *ModerationRepo) Users(ctx context.Context, after string, limit int) ([]entity.User, error) {
	rows, err := r.Pool.Query(ctx,
		_userSelect+` WHERE ($1 = '' OR u.id > NULLIF($1, '')::uuid) ORDER BY u.id LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("ModerationRepo - Users: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.User, error) {
		var u entity.User

		err := scanUser(row, &u)

		return u, err
	})
	if err != nil {
		return nil, fmt.Errorf("ModerationRepo - Users: %w", err)
	}

	return out, nil
}

// Blocks -.
func (r *ModerationRepo) Blocks(ctx context.Context, after *entity.BlockPair, limit int) ([]entity.BlockPair, error) {
	q := `SELECT blocker_id::text, blocked_id::text FROM user_blocks`
	args := []any{limit}

	if after != nil {
		q += ` WHERE (blocker_id, blocked_id) > ($2::uuid, $3::uuid)`

		args = append(args, after.BlockerID, after.BlockedID)
	}

	rows, err := r.Pool.Query(ctx, q+` ORDER BY blocker_id, blocked_id LIMIT $1`, args...)
	if err != nil {
		return nil, fmt.Errorf("ModerationRepo - Blocks: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.BlockPair, error) {
		var b entity.BlockPair

		return b, row.Scan(&b.BlockerID, &b.BlockedID)
	})
	if err != nil {
		return nil, fmt.Errorf("ModerationRepo - Blocks: %w", err)
	}

	return out, nil
}
