package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

const (
	_subCols = `s.id, s.user_id, s.channel_id, s.tier_id, s.status, s.current_period_start, s.current_period_end,
		s.auto_renew, s.cancelled_at, s.expired_reason, s.expiring_notified_for, s.created_at, ` + _channelCols + `, ` + _tierCols
	_subFrom = ` FROM subscriptions s JOIN channels c ON c.id = s.channel_id
		JOIN subscription_tiers t ON t.id = s.tier_id`
)

func scanSub(sc scanner) (entity.Subscription, error) {
	var s entity.Subscription

	err := sc.Scan(&s.ID, &s.UserID, &s.ChannelID, &s.TierID, &s.Status, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		&s.AutoRenew, &s.CancelledAt, &s.ExpiredReason, &s.ExpiringNotifiedFor, &s.CreatedAt,
		&s.Channel.ID, &s.Channel.OwnerID, &s.Channel.Handle, &s.Channel.DisplayName, &s.Channel.AvatarURL, &s.Channel.Status,
		&s.Tier.ID, &s.Tier.ChannelID, &s.Tier.Level, &s.Tier.Price, &s.Tier.Description, &s.Tier.Active, &s.Tier.UpdatedAt)

	return s, err
}

func subBy(ctx context.Context, q querier, where string, args ...any) (entity.Subscription, error) {
	s, err := scanSub(q.QueryRow(ctx, `SELECT `+_subCols+_subFrom+` WHERE `+where, args...))
	if err != nil {
		return s, fmt.Errorf("select subscription: %w", notFound(err))
	}

	return s, nil
}

func (r *Repo) subs(ctx context.Context, op, where string, args ...any) ([]entity.Subscription, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_subCols+_subFrom+` WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	out, err := collect(rows, scanSub)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return out, nil
}

// Mine -: current ones by end ascending, ended ones newest first; the cursor is (current_period_end, id).
func (r *Repo) Mine(ctx context.Context, userID string, f repo.SubFilter, after *repo.Cursor, limit int,
) ([]entity.Subscription, error) {
	set, at, id := pageArgs(after)

	if f.Expired {
		return r.subs(ctx, "Repo - Mine", `s.user_id = $1 AND ($2::uuid IS NULL OR s.channel_id = $2)
			AND (s.status = 'EXPIRED' OR s.current_period_end <= $3)
			AND (NOT $4 OR (s.current_period_end, s.id) < ($5, $6::uuid))
			ORDER BY s.current_period_end DESC, s.id DESC LIMIT $7`, userID, f.ChannelID, f.Now, set, at, id, limit)
	}

	return r.subs(ctx, "Repo - Mine", `s.user_id = $1 AND ($2::uuid IS NULL OR s.channel_id = $2)
		AND s.status IN `+_liveStatuses+` AND s.current_period_end > $3
		AND (NOT $4 OR (s.current_period_end, s.id) > ($5, $6::uuid))
		ORDER BY s.current_period_end, s.id LIMIT $7`, userID, f.ChannelID, f.Now, set, at, id, limit)
}

// Latest -.
func (r *Repo) Latest(ctx context.Context, userID, channelID string, now time.Time) (*entity.Subscription, error) {
	s, err := subBy(ctx, r.Pool, `s.user_id = $1 AND s.channel_id = $2
		ORDER BY (s.status IN `+_liveStatuses+` AND s.current_period_end > $3) DESC, s.current_period_end DESC, s.id DESC
		LIMIT 1`, userID, channelID, now)
	if errors.Is(err, entity.ErrNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("Repo - Latest: %w", err)
	}

	return &s, nil
}

// Get -.
func (r *Repo) Get(ctx context.Context, id string) (*entity.Subscription, error) {
	s, err := subBy(ctx, r.Pool, `s.id = $1`, id)
	if errors.Is(err, entity.ErrNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("Repo - Get: %w", err)
	}

	return &s, nil
}

// conditional runs a guarded UPDATE of the caller's subscription and writes out(updated); no row → 404 when the
// subscription is not the caller's, else 409.
func (r *Repo) conditional(ctx context.Context, op, set, guard, userID, subID string, now time.Time,
	out func(entity.Subscription) []repo.Outbox,
) (entity.Subscription, error) {
	var s entity.Subscription

	err := r.inTx(ctx, op, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE subscriptions SET `+set+`, updated_at = now()
			WHERE id = $1 AND user_id = $2 AND `+guard, subID, userID, now)
		if err != nil {
			return fmt.Errorf("%s - update: %w", op, err)
		}

		if s, err = subBy(ctx, tx, `s.id = $1 AND s.user_id = $2`, subID, userID); err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}

		if tag.RowsAffected() == 0 {
			return entity.ErrInvalidState
		}

		return insertOutbox(ctx, tx, out(s))
	})

	return s, err
}

// Cancel -: ACTIVE → CANCELLED_PENDING_END while the period runs.
func (r *Repo) Cancel(ctx context.Context, userID, subID string, now time.Time, out func(entity.Subscription) []repo.Outbox,
) (entity.Subscription, error) {
	return r.conditional(ctx, "Repo - Cancel", `status = 'CANCELLED_PENDING_END', auto_renew = false, cancelled_at = $3`,
		`status = 'ACTIVE' AND current_period_end > $3`, userID, subID, now, out)
}

// Resume -: CANCELLED_PENDING_END → ACTIVE while the period runs.
func (r *Repo) Resume(ctx context.Context, userID, subID string, now time.Time, out func(entity.Subscription) []repo.Outbox,
) (entity.Subscription, error) {
	return r.conditional(ctx, "Repo - Resume", `status = 'ACTIVE', auto_renew = true, cancelled_at = NULL`,
		`status = 'CANCELLED_PENDING_END' AND current_period_end > $3`, userID, subID, now, out)
}

// DueReminders -.
func (r *Repo) DueReminders(ctx context.Context, now time.Time, window time.Duration, limit int) ([]entity.Subscription, error) {
	return r.subs(ctx, "Repo - DueReminders", `s.status = 'ACTIVE' AND s.auto_renew AND s.current_period_end > $1
		AND s.current_period_end <= $2 AND s.expiring_notified_for IS DISTINCT FROM s.current_period_end
		ORDER BY s.current_period_end LIMIT $3`, now, now.Add(window), limit)
}

// MarkReminded -: once per period end.
func (r *Repo) MarkReminded(ctx context.Context, s entity.Subscription, out []repo.Outbox) (bool, error) {
	return r.guarded(ctx, "Repo - MarkReminded", `UPDATE subscriptions SET expiring_notified_for = $2, updated_at = now()
		WHERE id = $1 AND status = 'ACTIVE' AND current_period_end = $2 AND expiring_notified_for IS DISTINCT FROM $2`,
		s, out)
}

// DueExpiry -.
func (r *Repo) DueExpiry(ctx context.Context, now time.Time, limit int) ([]entity.Subscription, error) {
	return r.subs(ctx, "Repo - DueExpiry", `s.status IN `+_liveStatuses+` AND s.current_period_end <= $1
		ORDER BY s.current_period_end LIMIT $2`, now, limit)
}

// Expire -: only if the end did not move since it was read.
func (r *Repo) Expire(ctx context.Context, s entity.Subscription, out []repo.Outbox) (bool, error) {
	return r.guarded(ctx, "Repo - Expire", `UPDATE subscriptions SET status = 'EXPIRED', expired_reason = 'PERIOD_ENDED',
		updated_at = now() WHERE id = $1 AND status IN `+_liveStatuses+` AND current_period_end = $2`, s, out)
}

func (r *Repo) guarded(ctx context.Context, op, sql string, s entity.Subscription, out []repo.Outbox) (bool, error) {
	var done bool

	err := r.inTx(ctx, op, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, sql, s.ID, s.CurrentPeriodEnd)
		if err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}

		if done = tag.RowsAffected() == 1; !done {
			return nil
		}

		return insertOutbox(ctx, tx, out)
	})

	return done, err
}

// Live -.
func (r *Repo) Live(ctx context.Context, channelID *string, afterID string, now time.Time, limit int,
) ([]entity.Subscription, error) {
	if afterID == "" {
		afterID = "00000000-0000-0000-0000-000000000000"
	}

	return r.subs(ctx, "Repo - Live", `($1::uuid IS NULL OR s.channel_id = $1) AND s.status IN `+_liveStatuses+`
		AND s.current_period_end > $2 AND s.id > $3::uuid ORDER BY s.id LIMIT $4`, channelID, now, afterID, limit)
}

// CountActive -.
func (r *Repo) CountActive(ctx context.Context, now time.Time) (map[int]int, error) {
	rows, err := r.Pool.Query(ctx, `SELECT t.level, count(*) FROM subscriptions s
		JOIN subscription_tiers t ON t.id = s.tier_id
		WHERE s.status IN `+_liveStatuses+` AND s.current_period_end > $1 GROUP BY t.level`, now)
	if err != nil {
		return nil, fmt.Errorf("Repo - CountActive: %w", err)
	}

	return levelCounts(rows, "Repo - CountActive")
}
