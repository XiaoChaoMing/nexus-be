package moderation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// Payloads of the chat:{streamId} moderation events: ids and times only, never reasons or actors (BR-F27).
type (
	deletePayload struct {
		MessageID string `json:"messageId"`
	}
	clearedPayload struct {
		UserID string     `json:"userId"`
		Reason string     `json:"reason"`
		Until  *time.Time `json:"until"`
	}
	restoredPayload struct {
		UserID string `json:"userId"`
	}
	pinPayload struct {
		Message entity.Message `json:"message"`
	}
	unpinPayload struct{}
)

// DeleteMessage soft-deletes a message. The channel owner's messages are deleted by the owner or an admin
// only, another moderator's by the owner or an admin; anyone may delete their own (BR-F02, errata 11).
func (uc *UseCase) DeleteMessage(ctx context.Context, p entity.Principal, streamID, messageID string) error {
	sc, err := uc.room(ctx, streamID)
	if err != nil {
		return err
	}

	role, ch, err := uc.actor(ctx, p, sc.Room.ChannelID)
	if err != nil {
		return err
	}

	m, err := uc.r.Mod.Message(ctx, streamID, messageID)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return err
		}

		return fmt.Errorf("ModerationUseCase - DeleteMessage - read: %w", err)
	}

	if m.Deleted {
		return nil
	}

	if perr := uc.mayDelete(ctx, role, ch, p.UserID, m.User.ID); perr != nil {
		return perr
	}

	deleted, wasPinned, err := uc.r.Mod.DeleteMessage(ctx, entity.ModAction{
		ChannelID: ch.ID, StreamID: &streamID, ActorID: p.UserID, ActorRole: role, Type: entity.ModDelete,
		TargetUserID: &m.User.ID, MessageID: &messageID,
	})
	if err != nil {
		return fmt.Errorf("ModerationUseCase - DeleteMessage: %w", err)
	}

	if deleted {
		uc.relay(ctx, entity.ChatTopic(streamID), entity.EventDelete, deletePayload{MessageID: messageID})
	}

	if wasPinned {
		uc.relay(ctx, entity.ChatTopic(streamID), entity.EventUnpin, unpinPayload{})
	}

	return nil
}

func (uc *UseCase) mayDelete(ctx context.Context, role entity.ActorRole, ch entity.Channel, actorID, authorID string) error {
	if authorID == actorID || role == entity.ActorAdmin {
		return nil
	}

	if authorID == ch.OwnerID {
		return entity.Protected()
	}

	if role == entity.ActorOwner {
		return nil
	}

	author, err := uc.roleOf(ctx, ch.ID, authorID)
	if err != nil {
		return err
	}

	if author == entity.ChannelRoleModerator {
		return entity.Protected()
	}

	return nil
}

// Pin pins a message of an OPEN room; the previous pin is replaced (one pin event).
func (uc *UseCase) Pin(ctx context.Context, p entity.Principal, streamID, messageID string) (entity.Message, error) {
	sc, err := uc.room(ctx, streamID)
	if err != nil {
		return entity.Message{}, err
	}

	role, ch, err := uc.actor(ctx, p, sc.Room.ChannelID)
	if err != nil {
		return entity.Message{}, err
	}

	m, err := uc.r.Mod.Pin(ctx, entity.ModAction{
		ChannelID: ch.ID, StreamID: &streamID, ActorID: p.UserID, ActorRole: role, Type: entity.ModPin, MessageID: &messageID,
	})
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) || errors.Is(err, entity.ErrInvalidState) || errors.Is(err, entity.ErrRoomReadOnly) {
			return entity.Message{}, err
		}

		return entity.Message{}, fmt.Errorf("ModerationUseCase - Pin: %w", err)
	}

	m = uc.decorate(ctx, ch, m)
	uc.relay(ctx, entity.ChatTopic(streamID), entity.EventPin, pinPayload{Message: m})

	return m, nil
}

// decorate gives a pinned message its author's color and channel badges (subscriptions left out).
func (uc *UseCase) decorate(ctx context.Context, ch entity.Channel, m entity.Message) entity.Message {
	role, err := uc.roleOf(ctx, ch.ID, m.User.ID)
	if err != nil {
		uc.l.Warn("moderation - badges of %s: %v", m.User.ID, err)
	}

	st := entity.Standing{
		Streamer: m.User.ID == ch.OwnerID, Moderator: role == entity.ChannelRoleModerator, VIP: role == entity.ChannelRoleVIP,
	}
	m.User.Color, m.User.Badges = entity.ColorOf(m.User.ID), st.Badges()

	return m
}

// Unpin -.
func (uc *UseCase) Unpin(ctx context.Context, p entity.Principal, streamID string) error {
	sc, err := uc.room(ctx, streamID)
	if err != nil {
		return err
	}

	role, ch, err := uc.actor(ctx, p, sc.Room.ChannelID)
	if err != nil {
		return err
	}

	unpinned, err := uc.r.Mod.Unpin(ctx, entity.ModAction{
		ChannelID: ch.ID, StreamID: &streamID, ActorID: p.UserID, ActorRole: role, Type: entity.ModUnpin,
	})
	if err != nil {
		return fmt.Errorf("ModerationUseCase - Unpin: %w", err)
	}

	if unpinned {
		uc.relay(ctx, entity.ChatTopic(streamID), entity.EventUnpin, unpinPayload{})
	}

	return nil
}

// Timeout silences the user in the channel for d (1 s … CHAT_TIMEOUT_MAX); a later timeout replaces it.
func (uc *UseCase) Timeout(ctx context.Context, p entity.Principal, channelID, userID string, d time.Duration, reason *string,
) (entity.ActiveRestriction, error) {
	if d < time.Second || d > uc.cfg.TimeoutMax {
		detail := "MIN"
		if d > uc.cfg.TimeoutMax {
			detail = "MAX"
		}

		return entity.ActiveRestriction{}, entity.Reject(entity.ErrValidation, "durationSec", detail)
	}

	until := uc.now().Add(d).UTC()

	return uc.restrict(ctx, p, channelID, userID, entity.ModTimeout, reason, &until)
}

// Ban silences the user in the channel until unbanned.
func (uc *UseCase) Ban(ctx context.Context, p entity.Principal, channelID, userID string, reason *string) (entity.ActiveRestriction, error) {
	return uc.restrict(ctx, p, channelID, userID, entity.ModBan, reason, nil)
}

func (uc *UseCase) restrict(ctx context.Context, p entity.Principal, channelID, userID, typ string, reason *string, until *time.Time,
) (entity.ActiveRestriction, error) {
	reason, err := entity.CleanReason(reason)
	if err != nil {
		return entity.ActiveRestriction{}, err
	}

	role, ch, err := uc.actor(ctx, p, channelID)
	if err != nil {
		return entity.ActiveRestriction{}, err
	}

	if perr := uc.protected(ctx, role, ch, p.UserID, userID); perr != nil {
		return entity.ActiveRestriction{}, perr
	}

	if _, uerr := uc.knownUser(ctx, userID); uerr != nil {
		return entity.ActiveRestriction{}, uerr
	}

	rooms, unpinned, err := uc.r.Mod.Restrict(ctx, entity.ModAction{
		ChannelID: channelID, ActorID: p.UserID, ActorRole: role, Type: typ, TargetUserID: &userID, Reason: reason, ExpiresAt: until,
	})
	if err != nil {
		if errors.Is(err, entity.ErrInvalidState) {
			return entity.ActiveRestriction{}, err
		}

		return entity.ActiveRestriction{}, fmt.Errorf("ModerationUseCase - %s: %w", typ, err)
	}

	uc.relayCleared(ctx, userID, typ, until, rooms, unpinned)

	r, err := uc.r.Mod.Restriction(ctx, channelID, userID)
	if err != nil || r == nil {
		return entity.ActiveRestriction{}, fmt.Errorf("ModerationUseCase - %s - read back: %w", typ, err)
	}

	return *r, nil
}

func (uc *UseCase) relayCleared(ctx context.Context, userID, typ string, until *time.Time, rooms, unpinned []string) {
	cleared := clearedPayload{UserID: userID, Reason: "ban", Until: until}
	if typ == entity.ModTimeout {
		cleared.Reason = "timeout"
	}

	for _, s := range rooms {
		uc.relay(ctx, entity.ChatTopic(s), entity.EventUserCleared, cleared)
	}

	for _, s := range unpinned {
		uc.relay(ctx, entity.ChatTopic(s), entity.EventUnpin, unpinPayload{})
	}
}

// Untimeout lifts a running timeout; nothing to lift is not an error.
func (uc *UseCase) Untimeout(ctx context.Context, p entity.Principal, channelID, userID string) error {
	return uc.lift(ctx, p, channelID, userID, entity.ModUntimeout)
}

// Unban lifts a ban; nothing to lift is not an error.
func (uc *UseCase) Unban(ctx context.Context, p entity.Principal, channelID, userID string) error {
	return uc.lift(ctx, p, channelID, userID, entity.ModUnban)
}

func (uc *UseCase) lift(ctx context.Context, p entity.Principal, channelID, userID, typ string) error {
	role, _, err := uc.actor(ctx, p, channelID)
	if err != nil {
		return err
	}

	lifted, rooms, err := uc.r.Mod.Lift(ctx, entity.ModAction{
		ChannelID: channelID, ActorID: p.UserID, ActorRole: role, Type: typ, TargetUserID: &userID,
	})
	if err != nil {
		return fmt.Errorf("ModerationUseCase - %s: %w", typ, err)
	}

	if !lifted {
		return nil
	}

	for _, s := range rooms {
		uc.relay(ctx, entity.ChatTopic(s), entity.EventUserRestored, restoredPayload{UserID: userID})
	}

	return nil
}

// Restrictions lists the bans and running timeouts of the channel, newest first.
func (uc *UseCase) Restrictions(ctx context.Context, p entity.Principal, channelID string, t *entity.RestrictionType, cursor string,
	limit int,
) (usecase.RestrictionPage, error) {
	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.RestrictionPage{}, err
	}

	if _, _, err = uc.actor(ctx, p, channelID); err != nil {
		return usecase.RestrictionPage{}, err
	}

	limit = pageLimit(limit)

	items, err := uc.r.Mod.Restrictions(ctx, channelID, t, after, limit+1)
	if err != nil {
		return usecase.RestrictionPage{}, fmt.Errorf("ModerationUseCase - Restrictions: %w", err)
	}

	page := usecase.RestrictionPage{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}

	return page, nil
}

// Card shows a user from the channel's chat; the restriction only to those who may moderate it.
func (uc *UseCase) Card(ctx context.Context, p entity.Principal, channelID, userID string) (entity.UserCard, error) {
	ch, err := uc.r.Users.Channel(ctx, channelID)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return entity.UserCard{}, err
		}

		return entity.UserCard{}, fmt.Errorf("ModerationUseCase - Card - channel: %w", err)
	}

	u, err := uc.knownUser(ctx, userID)
	if err != nil {
		return entity.UserCard{}, err
	}

	card, err := uc.standingCard(ctx, ch, u)
	if err != nil {
		return entity.UserCard{}, err
	}

	if _, _, aerr := uc.actor(ctx, p, channelID); aerr != nil {
		return card, nil //nolint:nilerr // not a moderator: the card without the restriction
	}

	r, err := uc.r.Mod.Restriction(ctx, channelID, userID)
	if err != nil {
		return entity.UserCard{}, fmt.Errorf("ModerationUseCase - Card - restriction: %w", err)
	}

	if r != nil {
		card.Restriction = &entity.RestrictionState{Type: r.Type, Until: r.Until}
	}

	return card, nil
}

func (uc *UseCase) standingCard(ctx context.Context, ch entity.Channel, u entity.User) (entity.UserCard, error) {
	role, err := uc.roleOf(ctx, ch.ID, u.ID)
	if err != nil {
		return entity.UserCard{}, err
	}

	followedAt, tier, err := uc.r.Audience.Audience(ctx, ch.ID, u.ID)
	if err != nil {
		uc.l.Warn("moderation - card audience of %s: %v", u.ID, err)
	}

	st := entity.Standing{
		Streamer: u.ID == ch.OwnerID, Moderator: role == entity.ChannelRoleModerator, VIP: role == entity.ChannelRoleVIP, SubTier: tier,
	}

	card := entity.UserCard{
		User: entity.UserRef{ID: u.ID, Username: u.Username, DisplayName: u.Name()}, Badges: st.Badges(), FollowedAt: followedAt,
	}

	if !u.CreatedAt.IsZero() {
		createdAt := u.CreatedAt
		card.CreatedAt = &createdAt
	}

	switch {
	case st.Streamer:
		card.Role = string(entity.ActorOwner)
	case role != "":
		card.Role = string(role)
	}

	return card, nil
}
