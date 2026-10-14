// Package rooms reacts to domain events: the chat room lifecycle, the relays of stream events to
// stream:{streamId}, the copies of identity accounts and channel owners, and the follow/sub copies.
package rooms

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

const (
	// Daily partitions are created this many days ahead.
	_partitionDaysAhead = 3
	_day                = 24 * time.Hour
)

// Publisher is the realtime fan-out (fanout.Publisher).
type Publisher interface {
	Publish(ctx context.Context, topic entity.Topic, typ string, data any) (entity.Event, error)
}

// Repos groups the storage rooms needs.
type Repos struct {
	Rooms    repo.RoomRepo
	Messages repo.MessageRepo
	Users    repo.UserRepo
	Roles    repo.RoleRepo
	Audience repo.AudienceStore
	Presence repo.PresenceStore
}

// UseCase implements usecase.Rooms and usecase.Grants.
type UseCase struct {
	r         Repos
	pub       Publisher
	retention time.Duration
	l         logger.Interface
	now       func() time.Time
}

var (
	_ usecase.Rooms  = (*UseCase)(nil)
	_ usecase.Grants = (*UseCase)(nil)
)

// New -.
func New(r Repos, pub Publisher, retention time.Duration, l logger.Interface) *UseCase {
	return &UseCase{r: r, pub: pub, retention: retention, l: l, now: time.Now}
}

type roomStatePayload struct {
	Status entity.RoomStatus `json:"status"`
}

type statusPayload struct {
	Status    string  `json:"status"`
	EndReason *string `json:"endReason,omitempty"`
}

// StreamStarted opens the room. Relays are sent on every delivery: a duplicate realtime event is
// harmless, a missing one leaves clients on polling.
func (uc *UseCase) StreamStarted(ctx context.Context, e events.StreamStarted) error {
	if _, err := uc.r.Rooms.OpenRoom(ctx, entity.Room{StreamID: e.StreamID, ChannelID: e.ChannelID, Status: entity.RoomOpen}); err != nil {
		return fmt.Errorf("RoomsUseCase - StreamStarted: %w", err)
	}

	if err := uc.r.Presence.SetStreamStatus(ctx, e.StreamID, entity.StreamLive); err != nil {
		return fmt.Errorf("RoomsUseCase - StreamStarted - status: %w", err)
	}

	if err := uc.relay(ctx, entity.ChatTopic(e.StreamID), entity.EventRoomState, roomStatePayload{Status: entity.RoomOpen}); err != nil {
		return err
	}

	return uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventStatusChanged, statusPayload{Status: entity.StreamLive})
}

// StreamEnded makes the room read only and reports the peak viewer count to stream-svc.
func (uc *UseCase) StreamEnded(ctx context.Context, e events.StreamEnded) error {
	peak, err := uc.r.Presence.Peak(ctx, e.StreamID)
	if err != nil {
		return fmt.Errorf("RoomsUseCase - StreamEnded - peak: %w", err)
	}

	closed, err := uc.r.Rooms.CloseRoom(ctx, e.StreamID, peak)
	if err != nil {
		return fmt.Errorf("RoomsUseCase - StreamEnded: %w", err)
	}

	if closed {
		uc.l.Info("audit chat_room_state_changed stream=%s from=OPEN to=READ_ONLY reason=stream_ended peak=%d", e.StreamID, peak)
	}

	if serr := uc.r.Presence.SetStreamStatus(ctx, e.StreamID, entity.StreamEnded); serr != nil {
		return fmt.Errorf("RoomsUseCase - StreamEnded - status: %w", serr)
	}

	if rerr := uc.relay(ctx, entity.ChatTopic(e.StreamID), entity.EventRoomState, roomStatePayload{Status: entity.RoomReadOnly}); rerr != nil {
		return rerr
	}

	var reason *string
	if e.EndReason != "" {
		reason = &e.EndReason
	}

	return uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventStatusChanged, statusPayload{Status: entity.StreamEnded, EndReason: reason})
}

// StreamStatusChanged relays LIVE ↔ INTERRUPTED; the room stays OPEN (NXL-B §7A).
func (uc *UseCase) StreamStatusChanged(ctx context.Context, e events.StreamStatusChanged) error {
	if err := uc.r.Presence.SetStreamStatus(ctx, e.StreamID, e.Status); err != nil {
		return fmt.Errorf("RoomsUseCase - StreamStatusChanged: %w", err)
	}

	return uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventStatusChanged, statusPayload{Status: e.Status})
}

type titlePayload struct {
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

// StreamUpdated -.
func (uc *UseCase) StreamUpdated(ctx context.Context, e events.StreamUpdated) error {
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}

	return uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventTitleChanged, titlePayload{Title: e.Title, Tags: tags})
}

type vodPayload struct {
	VODID string `json:"vodId"`
}

// StreamVODReady -.
func (uc *UseCase) StreamVODReady(ctx context.Context, e events.StreamVODReady) error {
	return uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventVODReady, vodPayload{VODID: e.VODID})
}

func (uc *UseCase) relay(ctx context.Context, topic entity.Topic, typ string, data any) error {
	if _, err := uc.pub.Publish(ctx, topic, typ, data); err != nil {
		return fmt.Errorf("RoomsUseCase - relay %s on %s: %w", typ, topic, err)
	}

	return nil
}

// UserRegistered -.
func (uc *UseCase) UserRegistered(ctx context.Context, eventID string, u entity.User) error {
	if err := uc.r.Users.UpsertUser(ctx, eventID, u); err != nil {
		return fmt.Errorf("RoomsUseCase - UserRegistered: %w", err)
	}

	return nil
}

// UserProfileUpdated -.
func (uc *UseCase) UserProfileUpdated(ctx context.Context, eventID, userID, displayName string) error {
	if err := uc.r.Users.UpdateDisplayName(ctx, eventID, userID, displayName); err != nil {
		return fmt.Errorf("RoomsUseCase - UserProfileUpdated: %w", err)
	}

	return nil
}

// UserEmailVerified -.
func (uc *UseCase) UserEmailVerified(ctx context.Context, eventID, userID string) error {
	if err := uc.r.Users.MarkEmailVerified(ctx, eventID, userID); err != nil {
		return fmt.Errorf("RoomsUseCase - UserEmailVerified: %w", err)
	}

	return nil
}

// ChannelCreated -.
func (uc *UseCase) ChannelCreated(ctx context.Context, eventID string, c entity.Channel) error {
	if err := uc.r.Users.UpsertChannel(ctx, eventID, c); err != nil {
		return fmt.Errorf("RoomsUseCase - ChannelCreated: %w", err)
	}

	return nil
}

// MaintainPartitions creates today's and the next days' partitions and drops those past retention.
func (uc *UseCase) MaintainPartitions(ctx context.Context) error {
	today := uc.now().UTC().Truncate(_day)

	created, err := uc.r.Messages.EnsurePartitions(ctx, today, _partitionDaysAhead+1)
	if err != nil {
		return fmt.Errorf("RoomsUseCase - MaintainPartitions - create: %w", err)
	}

	dropped, err := uc.r.Messages.DropPartitionsBefore(ctx, today.Add(-uc.retention))
	if err != nil {
		return fmt.Errorf("RoomsUseCase - MaintainPartitions - drop: %w", err)
	}

	if len(created)+len(dropped) > 0 {
		uc.l.Info("chat partitions: created %v, dropped %v", created, dropped)
	}

	return nil
}

// ---- Grants --------------------------------------------------------------------------------------

// Follow -.
func (uc *UseCase) Follow(ctx context.Context, channelID, userID string, at time.Time) error {
	return wrap("Follow", uc.r.Audience.SetFollow(ctx, channelID, userID, at))
}

// Unfollow -.
func (uc *UseCase) Unfollow(ctx context.Context, channelID, userID string) error {
	return wrap("Unfollow", uc.r.Audience.RemoveFollow(ctx, channelID, userID))
}

// Subscribe -.
func (uc *UseCase) Subscribe(ctx context.Context, channelID, userID string, tier int) error {
	return wrap("Subscribe", uc.r.Audience.SetSub(ctx, channelID, userID, tier))
}

// Unsubscribe -.
func (uc *UseCase) Unsubscribe(ctx context.Context, channelID, userID string) error {
	return wrap("Unsubscribe", uc.r.Audience.RemoveSub(ctx, channelID, userID))
}

// GrantRole records the grant as made by the channel owner when known, else by the user (dev data).
func (uc *UseCase) GrantRole(ctx context.Context, channelID, userID string, role entity.ChannelRole) error {
	grantedBy := userID
	if ch, err := uc.r.Users.Channel(ctx, channelID); err == nil {
		grantedBy = ch.OwnerID
	}

	return wrap("GrantRole", uc.r.Roles.GrantRole(ctx, channelID, userID, role, grantedBy))
}

// RevokeRole -.
func (uc *UseCase) RevokeRole(ctx context.Context, channelID, userID string) error {
	return wrap("RevokeRole", uc.r.Roles.RevokeRole(ctx, channelID, userID))
}

// SetOwner -.
func (uc *UseCase) SetOwner(ctx context.Context, channelID, ownerID string) error {
	return wrap("SetOwner", uc.r.Users.UpsertChannel(ctx, "", entity.Channel{ID: channelID, OwnerID: ownerID}))
}

func wrap(op string, err error) error {
	if err != nil {
		return fmt.Errorf("RoomsUseCase - %s: %w", op, err)
	}

	return nil
}
