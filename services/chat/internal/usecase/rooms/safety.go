package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

const (
	// CloseAccountLocked closes the sockets of a locked account (NXL-00 §10.4).
	CloseAccountLocked = 4003
	_lockedReason      = "account_locked"

	_backfillChannels = "channels"
	_backfillPage     = 500
)

var _ usecase.Safety = (*UseCase)(nil)

// UserLocked remembers the lock (a still valid token cannot reconnect) and asks every instance to close the
// user's sockets. The order matters: a reconnect racing the close finds the lock already stored.
func (uc *UseCase) UserLocked(ctx context.Context, e events.UserLocked) error {
	if err := uc.r.Locks.Lock(ctx, e.UserID, e.LockedUntil); err != nil {
		return fmt.Errorf("RoomsUseCase - UserLocked: %w", err)
	}

	if err := uc.r.Broker.PublishControl(ctx, entity.Control{UserID: e.UserID, Code: CloseAccountLocked, Reason: _lockedReason}); err != nil {
		return fmt.Errorf("RoomsUseCase - UserLocked - control: %w", err)
	}

	return nil
}

// UserUnlocked -.
func (uc *UseCase) UserUnlocked(ctx context.Context, e events.UserUnlocked) error {
	return wrap("UserUnlocked", uc.r.Locks.Unlock(ctx, e.UserID))
}

// ChannelSuspended makes the channel's open rooms read only (stream-svc also ends the broadcast).
func (uc *UseCase) ChannelSuspended(ctx context.Context, eventID string, e events.ChannelSuspended) error {
	if err := uc.r.Users.SetChannelStatus(ctx, eventID, e.ChannelID, entity.ChannelSuspended); err != nil {
		return fmt.Errorf("RoomsUseCase - ChannelSuspended: %w", err)
	}

	streams, err := uc.r.Rooms.OpenRoomsOfChannel(ctx, e.ChannelID)
	if err != nil {
		return fmt.Errorf("RoomsUseCase - ChannelSuspended - rooms: %w", err)
	}

	for _, id := range streams {
		peak, perr := uc.r.Presence.Peak(ctx, id)
		if perr != nil {
			return fmt.Errorf("RoomsUseCase - ChannelSuspended - peak: %w", perr)
		}

		if _, cerr := uc.r.Rooms.CloseRoom(ctx, id, peak); cerr != nil {
			return fmt.Errorf("RoomsUseCase - ChannelSuspended - close: %w", cerr)
		}

		uc.l.Info("audit chat_room_state_changed stream=%s from=OPEN to=READ_ONLY reason=channel_suspended", id)

		if rerr := uc.relay(ctx, entity.ChatTopic(id), entity.EventRoomState, roomStatePayload{Status: entity.RoomReadOnly}); rerr != nil {
			return rerr
		}
	}

	return nil
}

// ChannelRestored -.
func (uc *UseCase) ChannelRestored(ctx context.Context, eventID string, e events.ChannelSuspended) error {
	return wrap("ChannelRestored", uc.r.Users.SetChannelStatus(ctx, eventID, e.ChannelID, entity.ChannelActive))
}

type notificationPayload struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
	Link       *string         `json:"link"`
	GroupCount int             `json:"groupCount"`
	Read       bool            `json:"read"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

type unreadPayload struct {
	Count int `json:"count"`
}

// RelayNotification sends the notification and the new unread count on user:{userId} when the user is online;
// offline users read them over REST.
func (uc *UseCase) RelayNotification(ctx context.Context, e events.NotificationCreated) error {
	if online, err := uc.online(ctx, e.UserID); err != nil || !online {
		return err
	}

	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	topic := entity.UserTopic(e.UserID)
	if err := uc.relay(ctx, topic, entity.EventNotification, notificationPayload{
		ID: e.NotificationID, Type: e.Type, Payload: payload, Link: e.Link, GroupCount: e.GroupCount, Read: e.Read,
		CreatedAt: e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC(),
	}); err != nil {
		return err
	}

	return uc.relay(ctx, topic, entity.EventUnreadCount, unreadPayload{Count: e.UnreadCount})
}

// RelayUnread -.
func (uc *UseCase) RelayUnread(ctx context.Context, e events.NotificationUnreadChanged) error {
	if online, err := uc.online(ctx, e.UserID); err != nil || !online {
		return err
	}

	return uc.relay(ctx, entity.UserTopic(e.UserID), entity.EventUnreadCount, unreadPayload{Count: e.UnreadCount})
}

func (uc *UseCase) online(ctx context.Context, userID string) (bool, error) {
	online, err := uc.r.Presence.Online(ctx, []string{userID})
	if err != nil {
		return false, fmt.Errorf("RoomsUseCase - online: %w", err)
	}

	return len(online) > 0, nil
}

// errNoSnapshot: the app wired no channel-svc snapshot client.
var errNoSnapshot = errors.New("no channel snapshot source")

// BackfillChannels copies channel-svc's channels (names, status) once; JetStream keeps 7 days of events only.
func (uc *UseCase) BackfillChannels(ctx context.Context) error {
	if uc.r.Snapshot == nil {
		return errNoSnapshot
	}

	done, err := uc.r.Users.BackfillDone(ctx, _backfillChannels)
	if err != nil || done {
		return wrap("BackfillChannels", err)
	}

	after, total := "", 0

	for {
		list, next, err := uc.r.Snapshot.Channels(ctx, after, _backfillPage)
		if err != nil {
			return fmt.Errorf("RoomsUseCase - BackfillChannels - page: %w", err)
		}

		if err = uc.r.Users.UpsertChannels(ctx, list); err != nil {
			return fmt.Errorf("RoomsUseCase - BackfillChannels - store: %w", err)
		}

		total += len(list)

		if next == "" {
			break
		}

		after = next
	}

	uc.l.Info("chat - backfilled %d channels", total)

	return wrap("BackfillChannels", uc.r.Users.MarkBackfilled(ctx, _backfillChannels))
}
