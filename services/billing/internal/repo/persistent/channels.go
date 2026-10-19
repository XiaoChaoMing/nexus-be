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
	_channelCols = `c.id, c.owner_id, c.handle, c.display_name, c.avatar_url, c.status`
	_tierCols    = `t.id, t.channel_id, t.level, t.price_vnd, COALESCE(t.benefits->>'description', ''), t.active, t.updated_at`
)

func scanChannel(s scanner) (entity.Channel, error) {
	var c entity.Channel

	err := s.Scan(&c.ID, &c.OwnerID, &c.Handle, &c.DisplayName, &c.AvatarURL, &c.Status)

	return c, err
}

func scanTier(s scanner) (entity.Tier, error) {
	var t entity.Tier

	err := s.Scan(&t.ID, &t.ChannelID, &t.Level, &t.Price, &t.Description, &t.Active, &t.UpdatedAt)

	return t, err
}

// Channel -.
func (r *Repo) Channel(ctx context.Context, id string) (entity.Channel, error) {
	c, err := scanChannel(r.Pool.QueryRow(ctx, `SELECT `+_channelCols+` FROM channels c WHERE c.id = $1`, id))
	if err != nil {
		return c, fmt.Errorf("Repo - Channel: %w", notFound(err))
	}

	return c, nil
}

// ByOwner -.
func (r *Repo) ByOwner(ctx context.Context, ownerID string) (entity.Channel, error) {
	c, err := scanChannel(r.Pool.QueryRow(ctx,
		`SELECT `+_channelCols+` FROM channels c WHERE c.owner_id = $1 ORDER BY c.synced_at LIMIT 1`, ownerID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c, entity.ErrNotCreator
		}

		return c, fmt.Errorf("Repo - ByOwner: %w", err)
	}

	return c, nil
}

// UpsertChannel -. Status "" keeps the stored status (ACTIVE for a new row).
func (r *Repo) UpsertChannel(ctx context.Context, eventID string, at time.Time, c entity.Channel, setAvatar bool,
	tiers []entity.Tier,
) error {
	return r.inEventTx(ctx, "Repo - UpsertChannel", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channels (id, owner_id, handle, display_name, avatar_url, status, synced_at)
			VALUES ($1, $2, $3, $4, $5, COALESCE(NULLIF($6, ''), 'ACTIVE'), $7)
			ON CONFLICT (id) DO UPDATE SET owner_id = EXCLUDED.owner_id, handle = EXCLUDED.handle,
				display_name = EXCLUDED.display_name,
				avatar_url = CASE WHEN $8 THEN EXCLUDED.avatar_url ELSE channels.avatar_url END,
				status = CASE WHEN $6 = '' THEN channels.status ELSE EXCLUDED.status END,
				synced_at = EXCLUDED.synced_at
			WHERE channels.synced_at <= EXCLUDED.synced_at`,
			c.ID, c.OwnerID, c.Handle, c.DisplayName, c.AvatarURL, c.Status, at, setAvatar); err != nil {
			return fmt.Errorf("Repo - UpsertChannel - upsert: %w", err)
		}

		for _, t := range tiers {
			if _, err := tx.Exec(ctx, `
				INSERT INTO subscription_tiers (channel_id, level, price_vnd, benefits, active)
				VALUES ($1, $2, $3, jsonb_build_object('description', $4::text), true)
				ON CONFLICT (channel_id, level) DO NOTHING`, c.ID, t.Level, t.Price, t.Description); err != nil {
				return fmt.Errorf("Repo - UpsertChannel - tier: %w", err)
			}
		}

		return nil
	})
}

// SetStatus -.
func (r *Repo) SetStatus(ctx context.Context, eventID string, at time.Time, channelID, status string) error {
	return r.inEventTx(ctx, "Repo - SetStatus", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE channels SET status = $2, synced_at = $3 WHERE id = $1 AND synced_at <= $3`,
			channelID, status, at); err != nil {
			return fmt.Errorf("Repo - SetStatus: %w", err)
		}

		return nil
	})
}

// Tier -.
func (r *Repo) Tier(ctx context.Context, id string) (entity.Tier, error) {
	t, err := scanTier(r.Pool.QueryRow(ctx, `SELECT `+_tierCols+` FROM subscription_tiers t WHERE t.id = $1`, id))
	if err != nil {
		return t, fmt.Errorf("Repo - Tier: %w", notFound(err))
	}

	return t, nil
}

// ByChannel -.
func (r *Repo) ByChannel(ctx context.Context, channelID string, activeOnly bool) ([]entity.Tier, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_tierCols+` FROM subscription_tiers t
		WHERE t.channel_id = $1 AND (t.active OR NOT $2) ORDER BY t.level`, channelID, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("Repo - ByChannel: %w", err)
	}

	out, err := collect(rows, scanTier)
	if err != nil {
		return nil, fmt.Errorf("Repo - ByChannel: %w", err)
	}

	return out, nil
}

// ActiveSubscribers -.
func (r *Repo) ActiveSubscribers(ctx context.Context, channelID string, now time.Time) (map[int]int, error) {
	rows, err := r.Pool.Query(ctx, `SELECT t.level, count(*) FROM subscriptions s
		JOIN subscription_tiers t ON t.id = s.tier_id
		WHERE s.channel_id = $1 AND s.status IN `+_liveStatuses+` AND s.current_period_end > $2 GROUP BY t.level`,
		channelID, now)
	if err != nil {
		return nil, fmt.Errorf("Repo - ActiveSubscribers: %w", err)
	}

	return levelCounts(rows, "Repo - ActiveSubscribers")
}

func levelCounts(rows pgx.Rows, op string) (map[int]int, error) {
	type pair struct{ level, n int }

	pairs, err := collect(rows, func(s scanner) (pair, error) {
		var p pair

		return p, s.Scan(&p.level, &p.n)
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	out := make(map[int]int, len(pairs))
	for _, p := range pairs {
		out[p.level] = p.n
	}

	return out, nil
}

// UpdateTier -.
func (r *Repo) UpdateTier(ctx context.Context, channelID string, level int, patch repo.TierPatch,
	out func(before, after entity.Tier) []repo.Outbox,
) (entity.Tier, error) {
	var after entity.Tier

	err := r.inTx(ctx, "Repo - UpdateTier", func(tx pgx.Tx) error {
		before, err := scanTier(tx.QueryRow(ctx, `SELECT `+_tierCols+` FROM subscription_tiers t
			WHERE t.channel_id = $1 AND t.level = $2 FOR UPDATE`, channelID, level))
		if err != nil {
			return fmt.Errorf("Repo - UpdateTier - select: %w", notFound(err))
		}

		after = before
		if patch.Active != nil {
			after.Active = *patch.Active
		}

		if patch.Description != nil {
			after.Description = *patch.Description
		}

		if after.Active == before.Active && after.Description == before.Description {
			return nil
		}

		if err := tx.QueryRow(ctx, `UPDATE subscription_tiers SET active = $2,
				benefits = jsonb_set(benefits, '{description}', to_jsonb($3::text)), updated_at = now()
			WHERE id = $1 RETURNING updated_at`, before.ID, after.Active, after.Description).Scan(&after.UpdatedAt); err != nil {
			return fmt.Errorf("Repo - UpdateTier - update: %w", err)
		}

		return insertOutbox(ctx, tx, out(before, after))
	})

	return after, err
}
