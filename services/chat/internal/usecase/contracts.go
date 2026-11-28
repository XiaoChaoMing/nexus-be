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
		// Emotes returns the channel's emotes (channelID set) before the global ones; an unknown channel has none.
		Emotes(ctx context.Context, channelID *string) ([]entity.Emote, error)
		// Snapshot is the chat:{streamId} snapshot: the room and what the caller may do.
		Snapshot(ctx context.Context, p entity.Principal, streamID string) (ChatSnapshot, error)
	}

	// Overlay is the OBS overlay of a channel: its look and the alerts the streamer sends to test it.
	Overlay interface {
		// Get returns the defaults for a channel that never saved any; entity.ErrNotFound for an unknown channel.
		Get(ctx context.Context, channelID string) (entity.OverlaySettings, error)
		// Update (channel owner or admin) replaces the options and pushes them to the open overlay pages.
		Update(ctx context.Context, p entity.Principal, s entity.OverlaySettings) (entity.OverlaySettings, error)
		// TestGift (channel owner or admin) shows a gift alert on the open overlay pages only.
		TestGift(ctx context.Context, p entity.Principal, channelID string, g entity.GiftMeta) error
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
		Subscribe(ctx context.Context, channelID, userID string, g entity.SubGrant) error
		Unsubscribe(ctx context.Context, channelID, userID string) error
		GrantRole(ctx context.Context, channelID, userID string, role entity.ChannelRole) error
		RevokeRole(ctx context.Context, channelID, userID string) error
		SetOwner(ctx context.Context, channelID, ownerID string) error
	}

	// Entitlements keeps the subscription copy from billing-svc's events (sub:{channelId}), announces new
	// subscribers in the channel's open rooms and relays payment results to the buyer (user:{userId}).
	Entitlements interface {
		// Activated stores the grant and, unless e.Replay, announces SUB_NEW once per eventID.
		Activated(ctx context.Context, eventID string, e events.SubscriptionActivated) error
		// Renewed stores the extended grant and announces SUB_RENEW once per eventID.
		Renewed(ctx context.Context, eventID string, e events.SubscriptionRenewed) error
		// Updated overwrites the grant (cancel / resume keep the period, a void may shorten it).
		Updated(ctx context.Context, e events.SubscriptionUpdated) error
		// Expired removes the grant unless a newer period replaced it.
		Expired(ctx context.Context, e events.SubscriptionExpired) error
		// PaymentUpdated sends payment_updated to the buyer when they are connected.
		PaymentUpdated(ctx context.Context, userID string, data entity.PaymentUpdate) error
		// GiftSent announces a gift once per eventID in the channel's open rooms.
		GiftSent(ctx context.Context, eventID string, e events.GiftSent) error
	}

	// Moderation is what the channel owner, its moderators and admins do in chat (NXL-F §3.1–3.6). Every
	// method resolves the caller's actor role in the channel and returns entity.ErrForbidden without one.
	Moderation interface {
		DeleteMessage(ctx context.Context, p entity.Principal, streamID, messageID string) error
		Pin(ctx context.Context, p entity.Principal, streamID, messageID string) (entity.Message, error)
		Unpin(ctx context.Context, p entity.Principal, streamID string) error
		Timeout(ctx context.Context, p entity.Principal, channelID, userID string, d time.Duration, reason *string) (entity.ActiveRestriction, error)
		Untimeout(ctx context.Context, p entity.Principal, channelID, userID string) error
		Ban(ctx context.Context, p entity.Principal, channelID, userID string, reason *string) (entity.ActiveRestriction, error)
		Unban(ctx context.Context, p entity.Principal, channelID, userID string) error
		Restrictions(ctx context.Context, p entity.Principal, channelID string, t *entity.RestrictionType, cursor string, limit int) (RestrictionPage, error)
		// Card is open to guests; the restriction is only shown to moderators.
		Card(ctx context.Context, p entity.Principal, channelID, userID string) (entity.UserCard, error)
		Terms(ctx context.Context, p entity.Principal, channelID string) ([]entity.BlockedTerm, error)
		// AddTerm returns created false with the existing term when it is already blocked.
		AddTerm(ctx context.Context, p entity.Principal, channelID, term string) (entity.BlockedTerm, bool, error)
		RemoveTerm(ctx context.Context, p entity.Principal, channelID, termID string) error
	}

	// Roles assigns moderators and VIPs (channel owner or admin) and lists the caller's channels.
	Roles interface {
		List(ctx context.Context, p entity.Principal, channelID string, role *entity.ChannelRole, cursor string, limit int) (RolePage, error)
		// Assign returns created true for a new grant.
		Assign(ctx context.Context, p entity.Principal, channelID string, who RoleTarget, role entity.ChannelRole) (entity.RoleGrant, bool, error)
		Revoke(ctx context.Context, p entity.Principal, channelID, userID string) error
		Mine(ctx context.Context, userID string) ([]entity.ModChannel, error)
	}

	// RoleTarget names the user of a role by id or by username (exactly one is set).
	RoleTarget struct {
		UserID   string
		Username string
	}

	// Evidence captures messages for safety-svc's reports (internal API).
	Evidence interface {
		Collect(ctx context.Context, streamID string, messageID, userID *string, limit int) ([]entity.EvidenceItem, error)
	}

	// Safety applies account locks, channel suspensions and relays notifications (event consumers).
	Safety interface {
		UserLocked(ctx context.Context, e events.UserLocked) error
		UserUnlocked(ctx context.Context, e events.UserUnlocked) error
		ChannelSuspended(ctx context.Context, eventID string, e events.ChannelSuspended) error
		ChannelRestored(ctx context.Context, eventID string, e events.ChannelSuspended) error
		RelayNotification(ctx context.Context, e events.NotificationCreated) error
		RelayUnread(ctx context.Context, e events.NotificationUnreadChanged) error
	}

	// RestrictionPage -.
	RestrictionPage struct {
		Items      []entity.ActiveRestriction
		NextCursor *string
	}

	// RolePage -.
	RolePage struct {
		Items      []entity.RoleGrant
		NextCursor *string
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

	// RoomSnapshot -. Pinned is the room's pinned message (null when none).
	RoomSnapshot struct {
		Status entity.RoomStatus `json:"status"`
		Mode   entity.Mode       `json:"mode"`
		Pinned *entity.Message   `json:"pinned"`
	}

	// MeSnapshot: reason is the error code a send would get now; retryAfterSec is set for slow mode.
	// CanModerate opens the moderation tools; Restriction is the caller's ban or running timeout.
	// SubTier is the caller's sub tier in force (0–3), EmoteTier the highest channel-emote tier they may send.
	MeSnapshot struct {
		Role          string                   `json:"role"`
		SubTier       int                      `json:"subTier"`
		EmoteTier     int                      `json:"emoteTier"`
		Badges        []string                 `json:"badges"`
		Color         string                   `json:"color"`
		CanSend       bool                     `json:"canSend"`
		CanModerate   bool                     `json:"canModerate"`
		Restriction   *entity.RestrictionState `json:"restriction"`
		Reason        *string                  `json:"reason,omitempty"`
		RetryAfterSec *int                     `json:"retryAfterSec,omitempty"`
	}

	// StreamSnapshot is the snapshot of stream:{streamId}. Status is null when chat-svc has not seen the stream.
	StreamSnapshot struct {
		Status      *string `json:"status"`
		ViewerCount int     `json:"viewerCount"`
	}
)
