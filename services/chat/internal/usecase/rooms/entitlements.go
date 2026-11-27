package rooms

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// Consumers that de-duplicate the announcements (processed_events).
const (
	_subSystemConsumer  = "chat-sub-system"
	_giftSystemConsumer = "chat-gift-system"
)

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
	return uc.announceOnce(ctx, eventID, _subSystemConsumer, channelID, userID, func(username string) any {
		return entity.NewSubAnnouncement(kind, userID, username, tier)
	})
}

// GiftSent announces a gift once per eventID in the channel's open rooms (none: nothing to show). A message the
// channel's AutoMod refuses, or that cannot be checked, is left out; the gift itself is still shown.
func (uc *UseCase) GiftSent(ctx context.Context, eventID string, e events.GiftSent) error {
	return uc.announceOnce(ctx, eventID, _giftSystemConsumer, e.ChannelID, e.UserID, func(username string) any {
		return entity.NewGiftAnnouncement(entity.GiftMeta{
			UserID: e.UserID, Username: username, ItemName: e.ItemName, Icon: e.Icon, Quantity: e.Quantity,
			Total: e.Total, Message: uc.screen(ctx, e.ChannelID, e.Message),
		})
	})
}

// screen applies the channel's blocked terms and link blocking to a gift message (viewer standing).
func (uc *UseCase) screen(ctx context.Context, channelID string, msg *string) *string {
	if msg == nil || uc.r.Settings == nil || uc.r.Terms == nil {
		return msg
	}

	settings, err := uc.r.Settings.Settings(ctx, channelID)
	if err != nil {
		uc.l.Warn("rooms - gift message settings %s: %v", channelID, err)

		return nil
	}

	terms, err := uc.r.Terms.BlockedTerms(ctx, channelID)
	if err != nil {
		uc.l.Warn("rooms - gift message terms %s: %v", channelID, err)

		return nil
	}

	rules := entity.AutoModRules{Terms: terms, BlockLinks: settings.BlockLinks}
	if rules.Check(*msg, entity.Standing{}) != nil {
		return nil
	}

	return msg
}

// announceOnce publishes build(username) as a `system` message in every open room of the channel, once per eventID.
func (uc *UseCase) announceOnce(ctx context.Context, eventID, consumer, channelID, userID string, build func(username string) any) error {
	streams, err := uc.r.Rooms.OpenRoomsOfChannel(ctx, channelID)
	if err != nil {
		return fmt.Errorf("RoomsUseCase - announceOnce - rooms: %w", err)
	}

	if len(streams) == 0 {
		return nil
	}

	if eventID != "" {
		first, cerr := uc.r.Events.Claim(ctx, eventID, consumer)
		if cerr != nil {
			return fmt.Errorf("RoomsUseCase - announceOnce - claim: %w", cerr)
		}

		if !first {
			return nil
		}
	}

	username := ""

	users, err := uc.r.Users.Users(ctx, []string{userID})
	if err != nil {
		uc.l.Warn("rooms - announcement user %s: %v", userID, err)
	} else if u, ok := users[userID]; ok {
		username = u.Username
	}

	msg := build(username)

	for _, id := range streams {
		if _, perr := uc.pub.Publish(ctx, entity.ChatTopic(id), entity.EventSystem, msg); perr != nil {
			uc.l.Warn("rooms - announcement on %s: %v", id, perr)
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
