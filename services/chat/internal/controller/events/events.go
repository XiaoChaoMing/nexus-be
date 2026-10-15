// Package events consumes the domain events chat-svc reacts to (JetStream durable consumer "chat-svc":
// with several instances each event is handled by one of them).
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// Durable is the JetStream consumer name of chat-svc.
const Durable = "chat-svc"

const (
	_maxUsername    = 25
	_maxDisplayName = 50
)

// Types consumed by chat-svc.
var Types = []string{ //nolint:gochecknoglobals // static list
	events.TypeStreamStarted, events.TypeStreamEnded, events.TypeStreamStatusChanged, events.TypeStreamUpdated,
	events.TypeStreamVODReady,
	events.TypeUserRegistered, events.TypeUserProfileUpdated, events.TypeUserEmailVerified,
	events.TypeChannelCreated, events.TypeChannelUpdated, events.TypeFollowCreated, events.TypeFollowUpdated,
	events.TypeFollowDeleted,
	events.TypeUserLocked, events.TypeUserUnlocked, events.TypeChannelSuspended, events.TypeChannelRestored,
	events.TypeNotificationCreated, events.TypeNotificationUnreadChanged,
}

// Handler routes one event to the rooms, grants or safety use case.
func Handler(rooms usecase.Rooms, grants usecase.Grants, safety usecase.Safety) eventbus.Handler {
	handlers := streamHandlers(rooms)
	for t, h := range copyHandlers(rooms, grants) {
		handlers[t] = h
	}

	for t, h := range safetyHandlers(safety) {
		handlers[t] = h
	}

	return func(ctx context.Context, e eventbus.Event) error {
		if h, ok := handlers[e.Type]; ok {
			return h(ctx, e)
		}

		return nil
	}
}

// handle decodes the payload as T and checks its uuids (ids) and the event id before calling fn;
// payloads that can never be processed are skipped instead of being redelivered forever.
func handle[T any](ids func(T) []string, fn func(ctx context.Context, eventID string, p T) error) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		var p T
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("%w: %s: %w", eventbus.ErrSkip, e.Type, err)
		}

		for _, id := range ids(p) {
			if _, err := uuid.Parse(id); err != nil {
				return fmt.Errorf("%w: %s: bad id", eventbus.ErrSkip, e.Type)
			}
		}

		if e.ID != "" {
			if _, err := uuid.Parse(e.ID); err != nil {
				return fmt.Errorf("%w: %s: event id is not a uuid", eventbus.ErrSkip, e.Type)
			}
		}

		return fn(ctx, e.ID, p)
	}
}

func streamHandlers(rooms usecase.Rooms) map[string]eventbus.Handler {
	return map[string]eventbus.Handler{
		events.TypeStreamStarted: handle(func(p events.StreamStarted) []string { return []string{p.StreamID, p.ChannelID} },
			func(ctx context.Context, _ string, p events.StreamStarted) error { return rooms.StreamStarted(ctx, p) }),
		events.TypeStreamEnded: handle(func(p events.StreamEnded) []string { return []string{p.StreamID} },
			func(ctx context.Context, _ string, p events.StreamEnded) error { return rooms.StreamEnded(ctx, p) }),
		events.TypeStreamStatusChanged: handle(func(p events.StreamStatusChanged) []string { return []string{p.StreamID} },
			func(ctx context.Context, _ string, p events.StreamStatusChanged) error {
				switch p.Status {
				case events.StreamStatusLive, events.StreamStatusInterrupted, events.StreamStatusCancelled, events.StreamStatusScheduled:
				default:
					return fmt.Errorf("%w: status %q", eventbus.ErrSkip, p.Status)
				}

				return rooms.StreamStatusChanged(ctx, p)
			}),
		events.TypeStreamUpdated: handle(func(p events.StreamUpdated) []string { return []string{p.StreamID} },
			func(ctx context.Context, _ string, p events.StreamUpdated) error { return rooms.StreamUpdated(ctx, p) }),
		events.TypeStreamVODReady: handle(func(p events.StreamVODReady) []string { return []string{p.StreamID, p.VODID} },
			func(ctx context.Context, _ string, p events.StreamVODReady) error {
				return rooms.StreamVODReady(ctx, p)
			}),
	}
}

// userRegistered copies the account; Nxl-Occurred-At of the event is its registration time.
func userRegistered(rooms usecase.Rooms) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		return handle(func(p events.UserRegistered) []string { return []string{p.UserID} },
			func(ctx context.Context, id string, p events.UserRegistered) error {
				if n := utf8.RuneCountInString(p.Username); n == 0 || n > _maxUsername || utf8.RuneCountInString(p.DisplayName) > _maxDisplayName {
					return fmt.Errorf("%w: bad names", eventbus.ErrSkip)
				}

				return rooms.UserRegistered(ctx, id, entity.User{
					ID: p.UserID, Username: p.Username, DisplayName: p.DisplayName, CreatedAt: e.OccurredAt.UTC(),
				})
			})(ctx, e)
	}
}

func copyHandlers(rooms usecase.Rooms, grants usecase.Grants) map[string]eventbus.Handler {
	return map[string]eventbus.Handler{
		events.TypeUserRegistered: userRegistered(rooms),
		events.TypeUserProfileUpdated: handle(func(p events.UserProfileUpdated) []string { return []string{p.UserID} },
			func(ctx context.Context, id string, p events.UserProfileUpdated) error {
				if utf8.RuneCountInString(p.DisplayName) > _maxDisplayName {
					return fmt.Errorf("%w: bad displayName", eventbus.ErrSkip)
				}

				return rooms.UserProfileUpdated(ctx, id, p.UserID, p.DisplayName)
			}),
		events.TypeUserEmailVerified: handle(func(p events.UserEmailVerified) []string { return []string{p.UserID} },
			func(ctx context.Context, id string, p events.UserEmailVerified) error {
				return rooms.UserEmailVerified(ctx, id, p.UserID)
			}),
		events.TypeChannelCreated: handle(func(p events.ChannelCreated) []string { return []string{p.ChannelID, p.OwnerID} },
			func(ctx context.Context, id string, p events.ChannelCreated) error {
				return rooms.ChannelCreated(ctx, id, channelOf(p.ChannelID, p.OwnerID, p.Handle, p.DisplayName, nil))
			}),
		events.TypeChannelUpdated: handle(func(p events.ChannelUpdated) []string { return []string{p.ChannelID, p.OwnerID} },
			func(ctx context.Context, id string, p events.ChannelUpdated) error {
				return rooms.ChannelCreated(ctx, id, channelOf(p.ChannelID, p.OwnerID, p.Handle, p.DisplayName, p.AvatarURL))
			}),
		events.TypeFollowCreated: handle(func(p events.FollowCreated) []string { return []string{p.UserID, p.ChannelID} },
			func(ctx context.Context, _ string, p events.FollowCreated) error {
				at := p.CreatedAt
				if at.IsZero() {
					at = time.Now()
				}

				level := p.NotifyLevel
				if level == "" {
					level = events.NotifyAll
				}

				return grants.Follow(ctx, p.ChannelID, p.UserID, at, level)
			}),
		events.TypeFollowUpdated: handle(func(p events.FollowUpdated) []string { return []string{p.UserID, p.ChannelID} },
			func(ctx context.Context, _ string, p events.FollowUpdated) error {
				switch p.NotifyLevel {
				case events.NotifyAll, events.NotifyLiveOnly, events.NotifyOff:
				default:
					return fmt.Errorf("%w: notifyLevel %q", eventbus.ErrSkip, p.NotifyLevel)
				}

				return grants.SetNotifyLevel(ctx, p.ChannelID, p.UserID, p.NotifyLevel)
			}),
		events.TypeFollowDeleted: handle(func(p events.FollowDeleted) []string { return []string{p.UserID, p.ChannelID} },
			func(ctx context.Context, _ string, p events.FollowDeleted) error {
				return grants.Unfollow(ctx, p.ChannelID, p.UserID)
			}),
	}
}

// channelOf drops names that do not fit chat.channels (the owner still matters).
func channelOf(id, owner, handle, name string, avatar *string) entity.Channel {
	if utf8.RuneCountInString(handle) > _maxUsername || utf8.RuneCountInString(name) > _maxDisplayName {
		handle, name = "", ""
	}

	return entity.Channel{ID: id, OwnerID: owner, Handle: handle, DisplayName: name, AvatarURL: avatar}
}

func safetyHandlers(safety usecase.Safety) map[string]eventbus.Handler {
	return map[string]eventbus.Handler{
		events.TypeUserLocked: handle(func(p events.UserLocked) []string { return []string{p.UserID} },
			func(ctx context.Context, _ string, p events.UserLocked) error { return safety.UserLocked(ctx, p) }),
		events.TypeUserUnlocked: handle(func(p events.UserUnlocked) []string { return []string{p.UserID} },
			func(ctx context.Context, _ string, p events.UserUnlocked) error { return safety.UserUnlocked(ctx, p) }),
		events.TypeChannelSuspended: handle(func(p events.ChannelSuspended) []string { return []string{p.ChannelID} },
			func(ctx context.Context, id string, p events.ChannelSuspended) error {
				return safety.ChannelSuspended(ctx, id, p)
			}),
		events.TypeChannelRestored: handle(func(p events.ChannelSuspended) []string { return []string{p.ChannelID} },
			func(ctx context.Context, id string, p events.ChannelSuspended) error {
				return safety.ChannelRestored(ctx, id, p)
			}),
		events.TypeNotificationCreated: handle(func(p events.NotificationCreated) []string { return []string{p.UserID, p.NotificationID} },
			func(ctx context.Context, _ string, p events.NotificationCreated) error {
				return safety.RelayNotification(ctx, p)
			}),
		events.TypeNotificationUnreadChanged: handle(func(p events.NotificationUnreadChanged) []string { return []string{p.UserID} },
			func(ctx context.Context, _ string, p events.NotificationUnreadChanged) error {
				return safety.RelayUnread(ctx, p)
			}),
	}
}
