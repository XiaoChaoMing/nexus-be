package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

var _ repo.FollowRepo = (*Repo)(nil)

func scanFollow(row scanner) (entity.Follow, error) {
	var (
		f     entity.Follow
		level string
	)

	err := row.Scan(&f.UserID, &f.ChannelID, &level, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Follow{}, entity.ErrNotFollowing
	}

	if err != nil {
		return entity.Follow{}, fmt.Errorf("scan follow: %w", err)
	}

	f.NotifyLevel = entity.NotifyLevel(level)

	return f, nil
}

const _followCols = `user_id::text, channel_id::text, notify_level, created_at`

// Follow -.
func (r *Repo) Follow(ctx context.Context, userID, channelID string) (entity.Follow, bool, error) {
	var (
		f       entity.Follow
		created bool
	)

	err := r.inTx(ctx, "Repo - Follow", func(tx pgx.Tx) error {
		var err error

		f, err = scanFollow(tx.QueryRow(ctx,
			`INSERT INTO follows (user_id, channel_id) VALUES ($1, $2) ON CONFLICT DO NOTHING RETURNING `+_followCols,
			userID, channelID))

		switch {
		case errors.Is(err, entity.ErrNotFollowing):
			f, err = scanFollow(tx.QueryRow(ctx,
				`SELECT `+_followCols+` FROM follows WHERE user_id = $1 AND channel_id = $2`, userID, channelID))
			if err != nil {
				return fmt.Errorf("Repo - Follow - existing: %w", err)
			}

			return nil
		case err != nil:
			return fmt.Errorf("Repo - Follow - insert: %w", err)
		}

		created = true

		if _, err = tx.Exec(ctx, `UPDATE channels SET follower_count = follower_count + 1 WHERE id = $1`, channelID); err != nil {
			return fmt.Errorf("Repo - Follow - count: %w", err)
		}

		return addOutbox(ctx, tx, "channel", channelID, events.TypeFollowCreated, events.FollowCreated{
			UserID: userID, ChannelID: channelID, CreatedAt: f.CreatedAt.UTC(), NotifyLevel: string(f.NotifyLevel),
		})
	})

	return f, created, err
}

// SetNotifyLevel -.
func (r *Repo) SetNotifyLevel(ctx context.Context, userID, channelID string, l entity.NotifyLevel) (entity.Follow, error) {
	var f entity.Follow

	err := r.inTx(ctx, "Repo - SetNotifyLevel", func(tx pgx.Tx) error {
		var (
			old string
			err error
		)

		if err = tx.QueryRow(ctx,
			`SELECT notify_level FROM follows WHERE user_id = $1 AND channel_id = $2 FOR UPDATE`, userID, channelID).Scan(&old); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return entity.ErrNotFollowing
			}

			return fmt.Errorf("Repo - SetNotifyLevel - select: %w", err)
		}

		f, err = scanFollow(tx.QueryRow(ctx,
			`UPDATE follows SET notify_level = $3, updated_at = now() WHERE user_id = $1 AND channel_id = $2
			 RETURNING `+_followCols, userID, channelID, string(l)))
		if err != nil {
			return fmt.Errorf("Repo - SetNotifyLevel - update: %w", err)
		}

		if old == string(l) {
			return nil
		}

		return addOutbox(ctx, tx, "channel", channelID, events.TypeFollowUpdated, events.FollowUpdated{
			UserID: userID, ChannelID: channelID, NotifyLevel: string(l), UpdatedAt: time.Now().UTC(),
		})
	})

	return f, err
}

// Unfollow -.
func (r *Repo) Unfollow(ctx context.Context, userID, channelID string) error {
	return r.inTx(ctx, "Repo - Unfollow", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM follows WHERE user_id = $1 AND channel_id = $2`, userID, channelID)
		if err != nil {
			return fmt.Errorf("Repo - Unfollow: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return nil
		}

		if _, err = tx.Exec(ctx,
			`UPDATE channels SET follower_count = GREATEST(follower_count - 1, 0) WHERE id = $1`, channelID); err != nil {
			return fmt.Errorf("Repo - Unfollow - count: %w", err)
		}

		return addOutbox(ctx, tx, "channel", channelID, events.TypeFollowDeleted,
			events.FollowDeleted{UserID: userID, ChannelID: channelID})
	})
}

// GetFollow -.
func (r *Repo) GetFollow(ctx context.Context, userID, channelID string) (*entity.Follow, error) {
	f, err := scanFollow(r.Pool.QueryRow(ctx,
		`SELECT `+_followCols+` FROM follows WHERE user_id = $1 AND channel_id = $2`, userID, channelID))
	if errors.Is(err, entity.ErrNotFollowing) {
		return nil, nil //nolint:nilnil // not following is not an error
	}

	if err != nil {
		return nil, fmt.Errorf("Repo - Get follow: %w", err)
	}

	return &f, nil
}

const _followedFrom = ` FROM follows f JOIN channels c ON c.id = f.channel_id WHERE f.user_id = $1 AND c.status = 'ACTIVE'`

// ListLive -.
func (r *Repo) ListLive(ctx context.Context, userID string, limit int) ([]entity.FollowedChannel, error) {
	return r.followed(ctx, "ListLive",
		`SELECT `+_channelCols+`, f.notify_level, f.created_at`+_followedFrom+` AND c.is_live LIMIT $2`, userID, limit)
}

// _lastLive orders never-live channels last without -infinity (which time.Time cannot hold).
const _lastLive = `COALESCE(c.last_live_at, 'epoch'::timestamptz)`

// ListFollowed -.
func (r *Repo) ListFollowed(ctx context.Context, userID string, offlineOnly bool, after *repo.FollowCursor, limit int) ([]entity.FollowedChannel, error) {
	q := `SELECT ` + _channelCols + `, f.notify_level, f.created_at` + _followedFrom
	args := []any{userID, limit}

	if offlineOnly {
		q += ` AND NOT c.is_live`
	}

	if after != nil {
		q += ` AND (c.is_live, ` + _lastLive + `, c.id) < ($3, $4, $5::uuid)`

		args = append(args, after.IsLive, after.LastLiveAt, after.ChannelID)
	}

	q += ` ORDER BY c.is_live DESC, ` + _lastLive + ` DESC, c.id DESC LIMIT $2`

	return r.followed(ctx, "ListFollowed", q, args...)
}

func (r *Repo) followed(ctx context.Context, op, q string, args ...any) ([]entity.FollowedChannel, error) {
	rows, err := r.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}
	defer rows.Close()

	out := []entity.FollowedChannel{}

	for rows.Next() {
		var (
			fc    entity.FollowedChannel
			level string
		)

		fc.Channel, err = scanChannel(rows, &level, &fc.FollowedAt)
		if err != nil {
			return nil, fmt.Errorf("Repo - %s: %w", op, err)
		}

		fc.NotifyLevel = entity.NotifyLevel(level)
		out = append(out, fc)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}

	return out, nil
}

// CountByUser -.
func (r *Repo) CountByUser(ctx context.Context, userID string) (int, error) {
	var n int

	if err := r.Pool.QueryRow(ctx, `SELECT count(*)`+_followedFrom, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("Repo - CountByUser: %w", err)
	}

	return n, nil
}

// NewFollowers -.
func (r *Repo) NewFollowers(ctx context.Context, channelID string, since time.Time) (int, error) {
	var n int

	if err := r.Pool.QueryRow(ctx,
		`SELECT count(*) FROM follows WHERE channel_id = $1 AND created_at >= $2`, channelID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("Repo - NewFollowers: %w", err)
	}

	return n, nil
}
