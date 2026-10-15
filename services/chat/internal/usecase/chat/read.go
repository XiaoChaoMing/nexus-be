package chat

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// System message kinds (event `system`, NXL-B BR21).
const (
	SystemModeChange = "MODE_CHANGE"
)

// History returns a page of the room's messages, newest first, with current badges and colors.
func (uc *UseCase) History(ctx context.Context, streamID string, before *string, limit int) ([]entity.Message, *string, error) {
	if limit <= 0 || limit > _maxHistory {
		limit = uc.cfg.HistoryDefault
	}

	sc, err := uc.r.Rooms.SendContext(ctx, streamID, "")
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return nil, nil, err
		}

		return nil, nil, fmt.Errorf("ChatUseCase - History - room: %w", err)
	}

	msgs, err := uc.r.Messages.History(ctx, streamID, before, limit+1)
	if err != nil {
		return nil, nil, fmt.Errorf("ChatUseCase - History: %w", err)
	}

	var next *string

	if len(msgs) > limit {
		msgs = msgs[:limit]
		id := msgs[limit-1].ID
		next = &id
	}

	uc.decorate(ctx, sc, msgs)

	for i := range msgs {
		msgs[i] = msgs[i].Blanked()
	}

	return msgs, next, nil
}

// decorate adds colors and badges from the current roles; a Redis failure only drops subscriber badges.
func (uc *UseCase) decorate(ctx context.Context, sc entity.SendContext, msgs []entity.Message) {
	ids := []string{}
	seen := map[string]bool{}

	for _, m := range msgs {
		if !seen[m.User.ID] {
			seen[m.User.ID] = true

			ids = append(ids, m.User.ID)
		}
	}

	roles, err := uc.r.Roles.Roles(ctx, sc.Room.ChannelID, ids)
	if err != nil {
		uc.l.Warn("chat - history roles: %v", err)
	}

	tiers, err := uc.r.Audience.SubTiers(ctx, sc.Room.ChannelID, ids)
	if err != nil {
		uc.l.Warn("chat - history sub tiers: %v", err)
	}

	for i := range msgs {
		id := msgs[i].User.ID
		st := entity.Standing{
			Streamer:  id == sc.OwnerID,
			Moderator: roles[id] == entity.ChannelRoleModerator,
			VIP:       roles[id] == entity.ChannelRoleVIP,
			SubTier:   tiers[id],
		}

		name := msgs[i].User
		msgs[i].User = author(id, nil, st)

		if name.Username != "" {
			msgs[i].User.Username, msgs[i].User.DisplayName = name.Username, name.DisplayName
		}
	}
}

// Settings returns the channel's modes; NOT_FOUND for a channel chat-svc never heard of.
func (uc *UseCase) Settings(ctx context.Context, channelID string) (entity.ChatSettings, error) {
	s, err := uc.r.Settings.Settings(ctx, channelID)
	if err != nil {
		return entity.ChatSettings{}, fmt.Errorf("ChatUseCase - Settings: %w", err)
	}

	if s.UpdatedAt != nil {
		return s, nil
	}

	if _, err = uc.r.Users.Channel(ctx, channelID); err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return entity.ChatSettings{}, err
		}

		return entity.ChatSettings{}, fmt.Errorf("ChatUseCase - Settings - channel: %w", err)
	}

	return s, nil
}

// UpdateSettings lets the channel owner, its moderators and admins change the modes (NXL-B API 04);
// a change is announced in every open room of the channel (AF05).
func (uc *UseCase) UpdateSettings(ctx context.Context, p entity.Principal, s entity.ChatSettings) (entity.ChatSettings, error) {
	if p.Guest() {
		return entity.ChatSettings{}, entity.ErrUnauthenticated
	}

	if err := s.Validate(); err != nil {
		return entity.ChatSettings{}, err
	}

	role, err := uc.canManage(ctx, p, s.ChannelID)
	if err != nil {
		return entity.ChatSettings{}, err
	}

	old, err := uc.r.Settings.Settings(ctx, s.ChannelID)
	if err != nil {
		return entity.ChatSettings{}, fmt.Errorf("ChatUseCase - UpdateSettings - read: %w", err)
	}

	s.UpdatedBy = &p.UserID

	var change *events.ChatSettingsChanged

	if !old.SameModes(s) {
		change = &events.ChatSettingsChanged{
			ChannelID: s.ChannelID, ActorID: p.UserID, ActorRole: string(role), Before: modesOf(old), After: modesOf(s),
		}
	}

	saved, err := uc.r.Settings.SaveSettings(ctx, s, change)
	if err != nil {
		return entity.ChatSettings{}, fmt.Errorf("ChatUseCase - UpdateSettings: %w", err)
	}

	uc.l.Info("audit chat_mode_changed channel=%s actor=%s old=%+v new=%+v", s.ChannelID, p.UserID, old.Mode(), saved.Mode())

	if !old.SameModes(saved) {
		uc.announceModes(ctx, saved)
	}

	return saved, nil
}

// canManage returns the caller's actor role in the channel: owner, admin or moderator.
func (uc *UseCase) canManage(ctx context.Context, p entity.Principal, channelID string) (entity.ActorRole, error) {
	ch, err := uc.r.Users.Channel(ctx, channelID)

	switch {
	case err == nil && ch.OwnerID == p.UserID:
		return entity.ActorOwner, nil
	case err != nil && !errors.Is(err, entity.ErrNotFound):
		return "", fmt.Errorf("ChatUseCase - canManage - channel: %w", err)
	case p.IsAdmin():
		return entity.ActorAdmin, nil
	}

	roles, err := uc.r.Roles.Roles(ctx, channelID, []string{p.UserID})
	if err != nil {
		return "", fmt.Errorf("ChatUseCase - canManage - roles: %w", err)
	}

	if roles[p.UserID] == entity.ChannelRoleModerator {
		return entity.ActorModerator, nil
	}

	return "", entity.ErrForbidden
}

func modesOf(s entity.ChatSettings) events.ChatModes {
	return events.ChatModes{SlowSec: s.SlowSec, FollowersMin: s.FollowersMin, SubsOnly: s.SubsOnly, BlockLinks: s.BlockLinks}
}

type systemPayload struct {
	Type string      `json:"type"`
	Text string      `json:"text"`
	Meta entity.Mode `json:"meta"`
}

func (uc *UseCase) announceModes(ctx context.Context, s entity.ChatSettings) {
	streams, err := uc.r.Rooms.OpenRoomsOfChannel(ctx, s.ChannelID)
	if err != nil {
		uc.l.Warn("chat - mode change rooms of %s: %v", s.ChannelID, err)

		return
	}

	for _, id := range streams {
		topic := entity.ChatTopic(id)

		if _, err = uc.pub.Publish(ctx, topic, entity.EventModeChange, s.Mode()); err != nil {
			uc.l.Warn("chat - mode_change on %s: %v", topic, err)

			continue
		}

		sys := systemPayload{Type: SystemModeChange, Text: "Chat modes changed", Meta: s.Mode()}
		if _, err = uc.pub.Publish(ctx, topic, entity.EventSystem, sys); err != nil {
			uc.l.Warn("chat - system on %s: %v", topic, err)
		}
	}
}

// Emotes returns the global emotes (channel emotes come later).
func (uc *UseCase) Emotes(ctx context.Context) ([]entity.Emote, error) {
	uc.emoteMu.Lock()
	defer uc.emoteMu.Unlock()

	if uc.emotes != nil && uc.now().Sub(uc.emoteAt) < _emoteCacheTTL {
		return uc.emotes, nil
	}

	list, err := uc.r.Emotes.GlobalEmotes(ctx)
	if err != nil {
		if uc.emotes != nil {
			uc.l.Warn("chat - emotes refresh: %v", err)

			return uc.emotes, nil
		}

		return nil, fmt.Errorf("ChatUseCase - Emotes: %w", err)
	}

	uc.emotes, uc.emoteAt = list, uc.now()

	return list, nil
}

func (uc *UseCase) emoteCodes(ctx context.Context) (map[string]entity.Emote, error) {
	list, err := uc.Emotes(ctx)
	if err != nil {
		return nil, err
	}

	out := make(map[string]entity.Emote, len(list))
	for _, e := range list {
		out[e.Code] = e
	}

	return out, nil
}

// Snapshot tells the client the room state and whether (and why not) it can send right now.
func (uc *UseCase) Snapshot(ctx context.Context, p entity.Principal, streamID string) (usecase.ChatSnapshot, error) {
	sc, st, err := uc.load(ctx, p, streamID)

	switch {
	case errors.Is(err, entity.ErrRoomClosed):
		snap := usecase.ChatSnapshot{Room: usecase.RoomSnapshot{Status: entity.RoomNotOpen, Mode: entity.ChatSettings{}.Mode()}}
		snap.Me = me(p, entity.Standing{})
		snap.Me.CanSend, snap.Me.Reason = false, reasonOf(entity.ErrRoomClosed)

		return snap, nil
	case err != nil:
		return usecase.ChatSnapshot{}, err
	}

	snap := usecase.ChatSnapshot{Room: usecase.RoomSnapshot{Status: sc.Room.Status, Mode: sc.Settings.Mode()}, Me: me(p, st)}
	snap.Me.Restriction = sc.Restriction.Current(uc.now())
	snap.Room.Pinned = uc.pinned(ctx, streamID)

	if err = uc.admit(p, sc, st); err != nil {
		snap.Me.Reason = reasonOf(err)

		return snap, nil
	}

	if sc.Settings.SlowSec > 0 && !st.SkipsSlow() {
		left, serr := uc.r.Guard.SlowLeft(ctx, streamID, p.UserID)
		if serr != nil {
			return usecase.ChatSnapshot{}, fmt.Errorf("ChatUseCase - Snapshot - slow: %w", serr)
		}

		if left > 0 {
			secs := int(math.Ceil(left.Seconds()))
			snap.Me.Reason, snap.Me.RetryAfterSec = reasonOf(entity.ErrSlowMode), &secs

			return snap, nil
		}
	}

	snap.Me.CanSend = true

	return snap, nil
}

// pinned degrades to no pin when it cannot be read: the snapshot stays usable.
func (uc *UseCase) pinned(ctx context.Context, streamID string) *entity.Message {
	if uc.pins == nil {
		return nil
	}

	m, err := uc.pins.Pinned(ctx, streamID)
	if err != nil {
		uc.l.Warn("chat - pinned of %s: %v", streamID, err)

		return nil
	}

	return m
}

func me(p entity.Principal, st entity.Standing) usecase.MeSnapshot {
	if p.Guest() {
		return usecase.MeSnapshot{Role: "GUEST", Badges: []string{}, Reason: reasonOf(entity.ErrUnauthenticated)}
	}

	return usecase.MeSnapshot{Role: st.Role(), Badges: st.Badges(), Color: entity.ColorOf(p.UserID), CanModerate: st.Privileged()}
}

func reasonOf(err error) *string {
	code := entity.CodeOf(err)

	return &code
}
