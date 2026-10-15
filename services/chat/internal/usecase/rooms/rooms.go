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
	Live     repo.LiveStore
	Locks    repo.LockStore
	Broker   repo.Broker
	Snapshot repo.ChannelSnapshot
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
	Status           string     `json:"status"`
	EndReason        *string    `json:"endReason,omitempty"`
	ScheduledStartAt *time.Time `json:"scheduledStartAt,omitempty"`
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

	if err := uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventStatusChanged, statusPayload{Status: entity.StreamLive}); err != nil {
		return err
	}

	started := e.StartedAt
	if started.IsZero() {
		started = uc.now().UTC()
	}

	live := entity.LiveInfo{StreamID: e.StreamID, Title: e.Title, CategoryID: e.CategoryID, StartedAt: started}
	if err := uc.r.Live.SetLive(ctx, e.ChannelID, live); err != nil {
		return fmt.Errorf("RoomsUseCase - StreamStarted - live: %w", err)
	}

	uc.announce(ctx, e.ChannelID, entity.EventChannelLive, channelLivePayload{ChannelID: e.ChannelID, Stream: liveStreamPayload{
		ID: e.StreamID, Title: e.Title, CategoryID: e.CategoryID,
	}})

	return nil
}

type liveStreamPayload struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	CategoryID *string `json:"categoryId"`
	// ViewerCount is always null here: counts follow in viewer_count_batch.
	ViewerCount *int `json:"viewerCount"`
}

type channelLivePayload struct {
	ChannelID string            `json:"channelId"`
	Stream    liveStreamPayload `json:"stream"`
}

type channelOfflinePayload struct {
	ChannelID  string    `json:"channelId"`
	StreamID   string    `json:"streamId"`
	LastLiveAt time.Time `json:"lastLiveAt"`
}

// announce sends a sidebar event to the channel's followers that have a connection open. It is best effort:
// a follower who misses it sees the change on the next GET /v1/me/following/sidebar.
func (uc *UseCase) announce(ctx context.Context, channelID, typ string, data any) {
	followers, err := uc.r.Audience.Followers(ctx, channelID)
	if err != nil {
		uc.l.Warn("rooms - sidebar %s of %s: followers: %v", typ, channelID, err)

		return
	}

	online, err := uc.r.Presence.Online(ctx, followers)
	if err != nil {
		uc.l.Warn("rooms - sidebar %s of %s: online: %v", typ, channelID, err)

		return
	}

	for _, userID := range online {
		if _, err = uc.pub.Publish(ctx, entity.SidebarTopic(userID), typ, data); err != nil {
			uc.l.Warn("rooms - sidebar %s to %s: %v", typ, userID, err)
		}
	}
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

	if err := uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventStatusChanged,
		statusPayload{Status: entity.StreamEnded, EndReason: reason}); err != nil {
		return err
	}

	if e.ChannelID == "" {
		return nil // event published before channelId was required
	}

	if err := uc.r.Live.ClearLive(ctx, e.ChannelID, e.StreamID); err != nil {
		return fmt.Errorf("RoomsUseCase - StreamEnded - live: %w", err)
	}

	uc.announce(ctx, e.ChannelID, entity.EventChannelOffline, channelOfflinePayload{
		ChannelID: e.ChannelID, StreamID: e.StreamID, LastLiveAt: uc.now().UTC(),
	})

	return nil
}

// StreamStatusChanged relays LIVE ↔ INTERRUPTED (the room stays OPEN, NXL-B §7A), a cancelled occurrence
// and a moved one (SCHEDULED with its new start) to the Upcoming page.
func (uc *UseCase) StreamStatusChanged(ctx context.Context, e events.StreamStatusChanged) error {
	if err := uc.r.Presence.SetStreamStatus(ctx, e.StreamID, e.Status); err != nil {
		return fmt.Errorf("RoomsUseCase - StreamStatusChanged: %w", err)
	}

	p := statusPayload{Status: e.Status}
	if e.Status == entity.StreamScheduled {
		p.ScheduledStartAt = e.ScheduledStartAt
	}

	return uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventStatusChanged, p)
}

type titlePayload struct {
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

// StreamUpdated relays the new title and keeps the sidebar's copy of the stream current.
func (uc *UseCase) StreamUpdated(ctx context.Context, e events.StreamUpdated) error {
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}

	if err := uc.relay(ctx, entity.StreamTopic(e.StreamID), entity.EventTitleChanged, titlePayload{Title: e.Title, Tags: tags}); err != nil {
		return err
	}

	if e.ChannelID == "" {
		return nil
	}

	live, err := uc.r.Live.Live(ctx, []string{e.ChannelID})
	if err != nil {
		return fmt.Errorf("RoomsUseCase - StreamUpdated - live: %w", err)
	}

	l, ok := live[e.ChannelID]
	if !ok || l.StreamID != e.StreamID {
		return nil
	}

	l.Title, l.CategoryID = e.Title, e.CategoryID

	return wrap("StreamUpdated - live", uc.r.Live.SetLive(ctx, e.ChannelID, l))
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
func (uc *UseCase) Follow(ctx context.Context, channelID, userID string, at time.Time, notifyLevel string) error {
	return wrap("Follow", uc.r.Audience.SetFollow(ctx, channelID, userID, at, notifyLevel))
}

// SetNotifyLevel -.
func (uc *UseCase) SetNotifyLevel(ctx context.Context, channelID, userID, notifyLevel string) error {
	return wrap("SetNotifyLevel", uc.r.Audience.SetNotifyLevel(ctx, channelID, userID, notifyLevel))
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
