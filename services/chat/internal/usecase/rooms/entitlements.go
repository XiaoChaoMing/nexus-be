package rooms

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// _subSystemConsumer de-duplicates the subscriber announcements (processed_events).
const _subSystemConsumer = "chat-sub-system"

// Activated stores the new grant; a replayed copy (republish-entitlements) is not announced.
func (uc *UseCase) Activated(ctx context.Context, eventID string, e events.SubscriptionActivated) error {
	if err := uc.setGrant(ctx, e.ChannelID, e.UserID, e.Tier, e.CurrentPeriodEnd); err != nil {
		return err
	}

	if e.Replay {
		return nil
	}

	return uc.announceSub(ctx, eventID, entity.SystemSubNew, e.ChannelID, e.UserID, e.Tier)
}

// Renewed -.
func (uc *UseCase) Renewed(ctx context.Context, eventID string, e events.SubscriptionRenewed) error {
	if err := uc.setGrant(ctx, e.ChannelID, e.UserID, e.Tier, e.CurrentPeriodEnd); err != nil {
		return err
	}

	return uc.announceSub(ctx, eventID, entity.SystemSubRenew, e.ChannelID, e.UserID, e.Tier)
}

// Updated overwrites the grant: a cancelled subscription keeps its period, a void may end it earlier.
func (uc *UseCase) Updated(ctx context.Context, e events.SubscriptionUpdated) error {
	return uc.setGrant(ctx, e.ChannelID, e.UserID, e.Tier, e.CurrentPeriodEnd)
}

// Expired removes the grant of the period that ended. A period that ran out ends at the stored until. A void
// ends the period early: the event carries the shortened end (about now), while the copy still holds the end
// before the void, at most one SubPeriod later; a newer purchase always ends after that.
func (uc *UseCase) Expired(ctx context.Context, e events.SubscriptionExpired) error {
	until := e.CurrentPeriodEnd
	if e.Reason == events.SubExpiredVoided {
		until = until.Add(entity.SubPeriod)
	}

	return wrap("Expired", uc.r.Audience.RemoveSubIfNotNewer(ctx, e.ChannelID, e.UserID, until))
}

func (uc *UseCase) setGrant(ctx context.Context, channelID, userID string, tier int, until time.Time) error {
	g := entity.SubGrant{Tier: tier, Until: until.UTC()}

	return wrap("SetGrant", uc.r.Audience.SetSub(ctx, channelID, userID, g))
}

// announceSub publishes the system message in every OPEN room of the channel, once per event. The event is
// claimed before publishing: a crash in between loses the message rather than repeating it.
func (uc *UseCase) announceSub(ctx context.Context, eventID, kind, channelID, userID string, tier int) error {
	streams, err := uc.r.Rooms.OpenRoomsOfChannel(ctx, channelID)
	if err != nil {
		return fmt.Errorf("RoomsUseCase - announceSub - rooms: %w", err)
	}

	if len(streams) == 0 {
		return nil
	}

	if eventID != "" {
		first, cerr := uc.r.Events.Claim(ctx, eventID, _subSystemConsumer)
		if cerr != nil {
			return fmt.Errorf("RoomsUseCase - announceSub - claim: %w", cerr)
		}

		if !first {
			return nil
		}
	}

	username := ""

	users, err := uc.r.Users.Users(ctx, []string{userID})
	if err != nil {
		uc.l.Warn("rooms - sub announcement user %s: %v", userID, err)
	} else if u, ok := users[userID]; ok {
		username = u.Username
	}

	msg := entity.NewSubAnnouncement(kind, userID, username, tier)

	for _, id := range streams {
		if _, perr := uc.pub.Publish(ctx, entity.ChatTopic(id), entity.EventSystem, msg); perr != nil {
			uc.l.Warn("rooms - sub announcement on %s: %v", id, perr)
		}
	}

	return nil
}

// PaymentUpdated -.
func (uc *UseCase) PaymentUpdated(ctx context.Context, userID string, data entity.PaymentUpdate) error {
	if online, err := uc.online(ctx, userID); err != nil || !online {
		return err
	}

	return uc.relay(ctx, entity.UserTopic(userID), entity.EventPaymentUpdated, data)
}
