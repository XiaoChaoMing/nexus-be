// Package entity holds chat-svc domain types (ERD chat, NXL-B, NXL-00 §10).
package entity

import (
	"encoding/json"
	"strings"
	"time"
)

// Global roles carried by the access token.
const (
	GlobalRoleAdmin = "ADMIN"
)

// Badges, highest first (NXL-B BR15).
const (
	BadgeStreamer  = "STREAMER"
	BadgeModerator = "MODERATOR"
	BadgeVIP       = "VIP"
	BadgeSubPrefix = "SUB_T" // SUB_T1..SUB_T3
	// MaxBadges is how many badges a message shows.
	MaxBadges = 3
)

// ChannelRole is a row of channel_roles.
type ChannelRole string

// Channel roles.
const (
	ChannelRoleModerator ChannelRole = "MODERATOR"
	ChannelRoleVIP       ChannelRole = "VIP"
)

// Principal is who is on the other end of a connection or request.
type Principal struct {
	// UserID is empty for guests.
	UserID     string
	GlobalRole string
	// AnonID is the guest's browser id (sent in the auth frame); empty when unknown.
	AnonID string
	// ConnID identifies the WebSocket connection; empty on REST.
	ConnID string
	IP     string
	// ExpiresAt is when the access token expires (zero for guests).
	ExpiresAt time.Time
}

// Guest reports whether the caller is not signed in.
func (p Principal) Guest() bool { return p.UserID == "" }

// IsAdmin -.
func (p Principal) IsAdmin() bool { return p.GlobalRole == GlobalRoleAdmin }

// ViewerKey identifies one person for viewer counting: several tabs or connections of the same
// account (or the same guest browser) count once.
func (p Principal) ViewerKey() string {
	switch {
	case p.UserID != "":
		return "u:" + p.UserID
	case p.AnonID != "":
		return "a:" + p.AnonID
	default:
		return "c:" + p.ConnID
	}
}

// RoomStatus is the ChatRoom state (NXL-B §7A).
type RoomStatus string

// Room statuses. NOT_OPEN is reported for streams chat-svc has no room for yet.
const (
	RoomNotOpen  RoomStatus = "NOT_OPEN"
	RoomOpen     RoomStatus = "OPEN"
	RoomReadOnly RoomStatus = "READ_ONLY"
	RoomClosed   RoomStatus = "CLOSED"
)

// Room is one chat room per stream.
type Room struct {
	StreamID  string
	ChannelID string
	Status    RoomStatus
}

// User is chat-svc's copy of an identity account.
type User struct {
	ID            string
	Username      string
	DisplayName   string
	EmailVerified bool
	// CreatedAt is when the account was registered; zero when the copy does not know.
	CreatedAt time.Time
}

// Name is what other viewers see.
func (u User) Name() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}

	return u.Username
}

// Channel is chat-svc's copy of a channel (channel.created / channel.updated). The names are empty for a
// channel only known through the dev CLI.
type Channel struct {
	ID          string
	OwnerID     string
	Handle      string
	DisplayName string
	AvatarURL   *string
}

// Emote is a global (channel emotes come later) emote.
type Emote struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	ImageURL string `json:"imageUrl"`
	Animated bool   `json:"animated"`
	Scope    string `json:"scope"`
}

// Fragment types.
const (
	FragmentText    = "text"
	FragmentEmote   = "emote"
	FragmentMention = "mention"
)

// Fragment is one piece of a message body: never HTML (NXL-B BR05). Stored as jsonb and sent as is.
type Fragment struct {
	Type   string    `json:"type"`
	Text   string    `json:"text"`
	Emote  *EmoteRef `json:"emote,omitempty"`
	UserID string    `json:"userId,omitempty"`
}

// EmoteRef is the emote of an emote fragment.
type EmoteRef struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	ImageURL string `json:"imageUrl"`
}

// Message types.
const (
	MessageNormal = "NORMAL"
	MessageSystem = "SYSTEM"
)

// Author is the sender as shown next to a message.
type Author struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Color       string   `json:"color"`
	Badges      []string `json:"badges"`
}

// Message is a chat message; its JSON form is the `message` event payload (TSD §5.1).
type Message struct {
	ID          string     `json:"id"`
	StreamID    string     `json:"streamId"`
	User        Author     `json:"user"`
	Body        string     `json:"body"`
	Fragments   []Fragment `json:"fragments"`
	Type        string     `json:"type"`
	CreatedAt   time.Time  `json:"createdAt"`
	Pinned      bool       `json:"pinned"`
	Deleted     bool       `json:"deleted"`
	ClientMsgID string     `json:"-"`
}

// Blanked hides the text of a deleted message (history, pins): moderators do not see it either.
func (m Message) Blanked() Message {
	if m.Deleted {
		m.Body, m.Fragments = "", []Fragment{}
	}

	return m
}

// Mentioned returns the users the message mentions, in order of appearance, without the sender and at most
// MentionNotifyMax.
func (m Message) Mentioned() []string {
	out := []string{}
	seen := map[string]bool{m.User.ID: true}

	for _, f := range m.Fragments {
		if f.Type != FragmentMention || seen[f.UserID] || len(out) == MentionNotifyMax {
			continue
		}

		seen[f.UserID] = true
		out = append(out, f.UserID)
	}

	return out
}

// Excerpt is the message as plain text, at most MentionExcerptMax runes.
func (m Message) Excerpt() string {
	r := []rune(strings.Join(strings.Fields(m.Body), " "))
	if len(r) > MentionExcerptMax {
		r = r[:MentionExcerptMax]
	}

	return string(r)
}

// MentionsUser reports whether userID is mentioned in the message.
func (m Message) MentionsUser(userID string) bool {
	for _, f := range m.Fragments {
		if f.Type == FragmentMention && f.UserID == userID {
			return true
		}
	}

	return false
}

// SendInput is a `send type=message` frame.
type SendInput struct {
	ClientMsgID string
	Body        string
}

// SendResult answers a send; Duplicate is true when the clientMsgId was already accepted (retry).
type SendResult struct {
	Message   Message
	Duplicate bool
}

// Event is one realtime event of a topic, in sequence order.
type Event struct {
	Topic string          `json:"topic"`
	Seq   int64           `json:"seq"`
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data"`
	TS    time.Time       `json:"ts"`
}

// Realtime event types (snake_case, NXL-00 §10.3).
const (
	EventMessage       = "message"
	EventAck           = "ack"
	EventSystem        = "system"
	EventModeChange    = "mode_change"
	EventRoomState     = "room_state"
	EventSampling      = "sampling"
	EventStatusChanged = "status_changed"
	EventViewerCount   = "viewer_count"
	EventTitleChanged  = "title_changed"
	EventVODReady      = "vod_ready"
	// sidebar:{userId}: a followed channel went live / offline; viewer_count_batch is local to each socket.
	EventChannelLive      = "channel_live"
	EventChannelOffline   = "channel_offline"
	EventViewerCountBatch = "viewer_count_batch"
	// chat:{streamId} moderation (GĐ5).
	EventDelete       = "delete"
	EventUserCleared  = "user_cleared"
	EventUserRestored = "user_restored"
	EventPin          = "pin"
	EventUnpin        = "unpin"
	// user:{userId}: relayed from notification-svc.
	EventNotification = "notification"
	EventUnreadCount  = "unread_count"
)

// Stream statuses relayed on stream:{id}.
const (
	StreamLive        = "LIVE"
	StreamInterrupted = "INTERRUPTED"
	StreamEnded       = "ENDED"
	StreamScheduled   = "SCHEDULED"
	StreamCancelled   = "CANCELLED"
)

// LiveInfo is chat-svc's record of a channel's stream on air (Redis rt:live).
type LiveInfo struct {
	StreamID   string    `json:"streamId"`
	Title      string    `json:"title"`
	CategoryID *string   `json:"categoryId"`
	StartedAt  time.Time `json:"startedAt"`
}

// SidebarCount is the live viewer count of one followed channel.
type SidebarCount struct {
	ChannelID string `json:"channelId"`
	StreamID  string `json:"streamId"`
	Count     int    `json:"count"`
}
