package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
)

const _snapshotPage = 500

// defaultTiers are the three tiers a channel starts with (BILLING_TIER_PRICES, BR-H02).
func (uc *UseCase) defaultTiers() []entity.Tier {
	out := make([]entity.Tier, 0, entity.TierLevels)
	for i, price := range uc.s.TierPrices {
		out = append(out, entity.Tier{Level: i + 1, Price: price, Description: entity.DefaultTierDescription(i + 1), Active: true})
	}

	return out
}

// ChannelCreated copies a new channel and gives it the default tiers.
func (uc *UseCase) ChannelCreated(ctx context.Context, eventID string, at time.Time, e events.ChannelCreated) error {
	err := uc.r.Channels.UpsertChannel(ctx, eventID, at, entity.Channel{
		ID: e.ChannelID, OwnerID: e.OwnerID, Handle: e.Handle, DisplayName: e.DisplayName,
	}, false, uc.defaultTiers())
	if err != nil {
		return fmt.Errorf("billing - ChannelCreated: %w", err)
	}

	return nil
}

// ChannelUpdated -. A channel billing never saw also gets its tiers (ON CONFLICT DO NOTHING).
func (uc *UseCase) ChannelUpdated(ctx context.Context, eventID string, at time.Time, e events.ChannelUpdated) error {
	err := uc.r.Channels.UpsertChannel(ctx, eventID, at, entity.Channel{
		ID: e.ChannelID, OwnerID: e.OwnerID, Handle: e.Handle, DisplayName: e.DisplayName, AvatarURL: e.AvatarURL,
	}, true, uc.defaultTiers())
	if err != nil {
		return fmt.Errorf("billing - ChannelUpdated: %w", err)
	}

	return nil
}

// ChannelStatus -.
func (uc *UseCase) ChannelStatus(ctx context.Context, eventID string, at time.Time, channelID string, suspended bool) error {
	status := entity.ChannelActive
	if suspended {
		status = entity.ChannelSuspended
	}

	if err := uc.r.Channels.SetStatus(ctx, eventID, at, channelID, status); err != nil {
		return fmt.Errorf("billing - ChannelStatus: %w", err)
	}

	return nil
}

// Backfill copies channel-svc's snapshot (channels created before billing listened) with their tiers.
func (uc *UseCase) Backfill(ctx context.Context) (int, error) {
	n, after := 0, ""

	for {
		page, next, err := uc.r.Snapshot.Channels(ctx, after, _snapshotPage)
		if err != nil {
			return n, fmt.Errorf("billing - Backfill: %w", err)
		}

		for _, c := range page {
			if err := uc.r.Channels.UpsertChannel(ctx, "", c.UpdatedAt, c.Channel, true, uc.defaultTiers()); err != nil {
				return n, fmt.Errorf("billing - Backfill: %w", err)
			}

			n++
		}

		if next == "" || len(page) == 0 {
			return n, nil
		}

		after = next
	}
}
