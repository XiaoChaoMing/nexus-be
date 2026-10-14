// Package repo declares chat-svc storage: PostgreSQL (nxl_chat), Redis and core NATS.
package repo

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

type (
	// RoomRepo owns chat_rooms (NXL-B §7A).
	RoomRepo interface {
		// SendContext reads the room, its channel's settings and owner, the user's role, restriction and
		// profile. entity.ErrNotFound when the stream has no room. userID may be empty (guest).
		SendContext(ctx context.Context, streamID, userID string) (entity.SendContext, error)
		// OpenRoom creates the room OPEN; it reports false when the room already existed.
		OpenRoom(ctx context.Context, r entity.Room) (bool, error)
		// CloseRoom moves an OPEN room to READ_ONLY and, in the same transaction, queues
		// stream.peak_viewers.v1; it reports false when the room was not OPEN (or does not exist).
		CloseRoom(ctx context.Context, streamID string, peakViewers int) (bool, error)
		// OpenRoomsOfChannel lists the stream ids of the channel's OPEN rooms.
		OpenRoomsOfChannel(ctx context.Context, channelID string) ([]string, error)
	}

	// MessageRepo owns chat_messages (partitioned by day).
	MessageRepo interface {
		// InsertMessage stores m and returns it with its id and creation time.
		InsertMessage(ctx context.Context, m entity.Message) (entity.Message, error)
		// History returns up to limit messages older than before (a message id; nil = newest), newest first.
		// Authors carry the user id and the names from the identity copy only.
		History(ctx context.Context, streamID string, before *string, limit int) ([]entity.Message, error)
		// EnsurePartitions creates the daily partitions of the days [from, from+days) that are missing,
		// moving rows of those days out of the DEFAULT partition; it returns the names created.
		EnsurePartitions(ctx context.Context, from time.Time, days int) ([]string, error)
		// DropPartitionsBefore detaches and drops daily partitions of days before the given day.
		DropPartitionsBefore(ctx context.Context, day time.Time) ([]string, error)
	}

	// SettingsRepo owns chat_settings.
	SettingsRepo interface {
		// Settings returns defaults (UpdatedAt nil) when the channel never saved any.
		Settings(ctx context.Context, channelID string) (entity.ChatSettings, error)
		SaveSettings(ctx context.Context, s entity.ChatSettings) (entity.ChatSettings, error)
	}

	// RoleRepo owns channel_roles.
	RoleRepo interface {
		// Roles returns the roles of the given users in the channel.
		Roles(ctx context.Context, channelID string, userIDs []string) (map[string]entity.ChannelRole, error)
		GrantRole(ctx context.Context, channelID, userID string, role entity.ChannelRole, grantedBy string) error
		RevokeRole(ctx context.Context, channelID, userID string) error
	}

	// EmoteRepo -.
	EmoteRepo interface {
		GlobalEmotes(ctx context.Context) ([]entity.Emote, error)
	}

	// UserRepo is the copy of identity accounts (users) and channel owners (channels).
	// eventID de-duplicates through processed_events (empty = no de-dup).
	UserRepo interface {
		Users(ctx context.Context, ids []string) (map[string]entity.User, error)
		// UserIDsByUsername maps lower-cased usernames to user ids; unknown names are absent.
		UserIDsByUsername(ctx context.Context, names []string) (map[string]string, error)
		UpsertUser(ctx context.Context, eventID string, u entity.User) error
		UpdateDisplayName(ctx context.Context, eventID, userID, displayName string) error
		MarkEmailVerified(ctx context.Context, eventID, userID string) error
		// Channel returns entity.ErrNotFound for a channel never announced.
		Channel(ctx context.Context, channelID string) (entity.Channel, error)
		UpsertChannel(ctx context.Context, eventID string, c entity.Channel) error
	}

	// SeqStore numbers the events of each topic and keeps the resume buffer (Redis).
	SeqStore interface {
		// Append gives ev the topic's next seq and stores it in the resume buffer.
		Append(ctx context.Context, ev entity.Event) (entity.Event, error)
		// Seq is the topic's last seq (0 when it has none).
		Seq(ctx context.Context, topic string) (int64, error)
		// Since returns up to limit buffered events with seq > after, oldest first. complete is false when
		// the buffer no longer holds seq after+1 (expired or trimmed).
		Since(ctx context.Context, topic string, after int64, limit int) ([]entity.Event, bool, error)
	}

	// SendGuard holds the per-user state that rejects messages (Redis).
	SendGuard interface {
		// AcceptedMessage returns the message id stored for a clientMsgId already accepted.
		AcceptedMessage(ctx context.Context, userID, clientMsgID string) (string, bool, error)
		// RememberMessage stores the clientMsgId → message id and the body hash for duplicate checks.
		RememberMessage(ctx context.Context, userID, streamID, clientMsgID, messageID, bodyHash string) error
		// LastBodyHash returns the hash of the user's last body in the stream inside the duplicate window.
		LastBodyHash(ctx context.Context, userID, streamID string) (string, error)
		// Take removes one token from the bucket key (capacity tokens per window); retryAfter when empty.
		Take(ctx context.Context, key string, capacity int, window time.Duration) (bool, time.Duration, error)
		// StartSlow sets the user's slow-mode marker for d; when it is already set it returns false and
		// the time left.
		StartSlow(ctx context.Context, streamID, userID string, d time.Duration) (bool, time.Duration, error)
		// SlowLeft is the time left on the marker (0 when none).
		SlowLeft(ctx context.Context, streamID, userID string) (time.Duration, error)
	}

	// AudienceStore is the copy of follows and subscriptions (Redis follow:{channelId}, sub:{channelId}).
	AudienceStore interface {
		// Audience returns when userID followed the channel (nil = not following) and their sub tier (0 = none).
		Audience(ctx context.Context, channelID, userID string) (*time.Time, int, error)
		SubTiers(ctx context.Context, channelID string, userIDs []string) (map[string]int, error)
		// SetFollow records the follow in both directions (follow:{channelId}, following:{userId}) with its bell.
		SetFollow(ctx context.Context, channelID, userID string, at time.Time, notifyLevel string) error
		RemoveFollow(ctx context.Context, channelID, userID string) error
		SetNotifyLevel(ctx context.Context, channelID, userID, notifyLevel string) error
		// Followers lists the users following the channel.
		Followers(ctx context.Context, channelID string) ([]string, error)
		// Following lists the channels the user follows.
		Following(ctx context.Context, userID string) ([]string, error)
		SetSub(ctx context.Context, channelID, userID string, tier int) error
		RemoveSub(ctx context.Context, channelID, userID string) error
	}

	// PresenceStore tracks connections and viewers (Redis).
	PresenceStore interface {
		// AddConn registers a connection of the user; false when the user already has max connections.
		AddConn(ctx context.Context, userID, connID string, maxConns int) (bool, error)
		TouchConn(ctx context.Context, userID, connID string) error
		RemoveConn(ctx context.Context, userID, connID string) error

		// TouchViewers marks viewer keys as watching streamID now.
		TouchViewers(ctx context.Context, streamID string, keys []string, now time.Time) error
		// CountViewers counts viewer keys of streamID seen after since.
		CountViewers(ctx context.Context, streamID string, since time.Time) (int, error)
		// LeadViewers takes or renews the counting lease of instance; false when another instance holds it.
		LeadViewers(ctx context.Context, instance string, ttl time.Duration) (bool, error)
		// WatchedStreams lists the streams that had viewers recently.
		WatchedStreams(ctx context.Context) ([]string, error)
		// RefreshCount drops viewers older than cutoff, stores the count (viewers:{streamId}, ttl) and the
		// peak; changed reports a count different from the stored one.
		RefreshCount(ctx context.Context, streamID string, cutoff time.Time, ttl time.Duration) (int, bool, error)
		// Peak is the highest count stored for the stream.
		Peak(ctx context.Context, streamID string) (int, error)
		// ForgetStream removes the stream's viewer keys.
		ForgetStream(ctx context.Context, streamID string) error

		SetStreamStatus(ctx context.Context, streamID, status string) error
		// StreamStatus returns "" when unknown.
		StreamStatus(ctx context.Context, streamID string) (string, error)

		// Online returns the users among userIDs that have an open connection.
		Online(ctx context.Context, userIDs []string) ([]string, error)
		// ViewerCounts reads viewers:{streamId}; streams without a count are absent.
		ViewerCounts(ctx context.Context, streamIDs []string) (map[string]int, error)
	}

	// LiveStore is the map of channels on air (rt:live) behind the sidebar topics.
	LiveStore interface {
		SetLive(ctx context.Context, channelID string, l entity.LiveInfo) error
		// ClearLive removes the channel unless a newer stream replaced streamID.
		ClearLive(ctx context.Context, channelID, streamID string) error
		// Live returns the streams on air of the given channels.
		Live(ctx context.Context, channelIDs []string) (map[string]entity.LiveInfo, error)
	}

	// Broker fans realtime events out to every chat-svc instance (core NATS, no persistence).
	Broker interface {
		Publish(ctx context.Context, topic string, payload []byte) error
		// Subscribe calls fn for each payload published on topic until the returned stop is called.
		Subscribe(topic string, fn func(payload []byte)) (func(), error)
	}
)
