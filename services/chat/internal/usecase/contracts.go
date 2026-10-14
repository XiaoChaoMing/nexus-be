// Package usecase declares chat-svc use cases.
package usecase

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

type (
	// Gateway is what the realtime gateway needs besides chat: identity, topic snapshots, sequence
	// numbers, the resume buffer and the fan-out subscription.
	Gateway interface {
		// Authorize verifies the access token (nil = guest). entity.ErrInvalidToken when it does not verify.
		Authorize(ctx context.Context, token *string, anonID string) (entity.Principal, error)
		// ConnOpened enforces WS_MAX_CONN_PER_USER (entity.ErrTooManyConnections) for signed-in users.
		ConnOpened(ctx context.Context, p entity.Principal) error
		ConnAlive(ctx context.Context, p entity.Principal)
		ConnClosed(ctx context.Context, p entity.Principal)
		// Join checks access to topic, counts the caller as a viewer of chat:/stream: topics and returns
		// the topic's snapshot (JSON-encodable, nil when the topic has none).
		Join(ctx context.Context, p entity.Principal, topic entity.Topic) (any, error)
		// Leave undoes Join's viewer count.
		Leave(p entity.Principal, topic entity.Topic)
		Seq(ctx context.Context, topic string) (int64, error)
		Since(ctx context.Context, topic string, after int64, limit int) ([]entity.Event, bool, error)
		// Listen delivers the topic's events published by any instance until stop is called.
		Listen(topic string, fn func(entity.Event)) (stop func(), err error)
		// SidebarCounts returns the viewer counts of the user's followed channels that are live.
		SidebarCounts(ctx context.Context, userID string) ([]entity.SidebarCount, error)
	}

	// Chat is B1–B3: sending, history, emotes and chat modes.
	Chat interface {
		Send(ctx context.Context, p entity.Principal, streamID string, in entity.SendInput) (entity.SendResult, error)
		// History is newest first; next is the cursor of the following page (nil on the last one).
		History(ctx context.Context, streamID string, before *string, limit int) ([]entity.Message, *string, error)
		Settings(ctx context.Context, channelID string) (entity.ChatSettings, error)
		UpdateSettings(ctx context.Context, p entity.Principal, s entity.ChatSettings) (entity.ChatSettings, error)
		Emotes(ctx context.Context) ([]entity.Emote, error)
		// Snapshot is the chat:{streamId} snapshot: the room and what the caller may do.
		Snapshot(ctx context.Context, p entity.Principal, streamID string) (ChatSnapshot, error)
	}

	// Rooms reacts to domain events: room lifecycle, stream relays and the identity/channel copies.
	// eventID de-duplicates the copies.
	Rooms interface {
		StreamStarted(ctx context.Context, e events.StreamStarted) error
		StreamEnded(ctx context.Context, e events.StreamEnded) error
		StreamStatusChanged(ctx context.Context, e events.StreamStatusChanged) error
		StreamUpdated(ctx context.Context, e events.StreamUpdated) error
		StreamVODReady(ctx context.Context, e events.StreamVODReady) error
		UserRegistered(ctx context.Context, eventID string, u entity.User) error
		UserProfileUpdated(ctx context.Context, eventID, userID, displayName string) error
		UserEmailVerified(ctx context.Context, eventID, userID string) error
		ChannelCreated(ctx context.Context, eventID string, c entity.Channel) error
		// MaintainPartitions creates the next daily partitions of chat_messages and drops expired ones.
		MaintainPartitions(ctx context.Context) error
	}

	// Grants keeps the follow/sub copies and channel roles (follow.* events and the dev CLI).
	Grants interface {
		Follow(ctx context.Context, channelID, userID string, at time.Time, notifyLevel string) error
		SetNotifyLevel(ctx context.Context, channelID, userID, notifyLevel string) error
		Unfollow(ctx context.Context, channelID, userID string) error
		Subscribe(ctx context.Context, channelID, userID string, tier int) error
		Unsubscribe(ctx context.Context, channelID, userID string) error
		GrantRole(ctx context.Context, channelID, userID string, role entity.ChannelRole) error
		RevokeRole(ctx context.Context, channelID, userID string) error
		SetOwner(ctx context.Context, channelID, ownerID string) error
	}

	// Viewers counts people watching each stream (one per account or guest browser).
	Viewers interface {
		// Join counts key as watching streamID and returns the current count.
		Join(ctx context.Context, streamID, key string) (int, error)
		Leave(streamID, key string)
		// Tick refreshes this instance's viewers; the instance holding the lease publishes the counts.
		Tick(ctx context.Context) error
	}

	// Observer receives counters for /metrics; use cases never depend on a metrics library.
	Observer interface {
		MessageSent()
		Rejected(code string)
	}

	// ChatSnapshot is the snapshot of chat:{streamId} (TSD §5.1).
	ChatSnapshot struct {
		Room RoomSnapshot `json:"room"`
		Me   MeSnapshot   `json:"me"`
	}

	// RoomSnapshot -.
	RoomSnapshot struct {
		Status entity.RoomStatus `json:"status"`
		Mode   entity.Mode       `json:"mode"`
	}

	// MeSnapshot: reason is the error code a send would get now; retryAfterSec is set for slow mode.
	MeSnapshot struct {
		Role          string   `json:"role"`
		Badges        []string `json:"badges"`
		Color         string   `json:"color"`
		CanSend       bool     `json:"canSend"`
		Reason        *string  `json:"reason,omitempty"`
		RetryAfterSec *int     `json:"retryAfterSec,omitempty"`
	}

	// StreamSnapshot is the snapshot of stream:{streamId}. Status is null when chat-svc has not seen the stream.
	StreamSnapshot struct {
		Status      *string `json:"status"`
		ViewerCount int     `json:"viewerCount"`
	}
)
