// Package response holds the response DTOs of chat-svc's public REST API.
package response

import "time"

// Data wraps every successful body.
type Data[T any] struct {
	Data T `json:"data"`
}

// Error wraps every error body.
type Error struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody -.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// Author is the sender of a message.
type Author struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Color       string   `json:"color"`
	Badges      []string `json:"badges"`
}

// EmoteRef is the emote of an emote fragment.
type EmoteRef struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	ImageURL string `json:"imageUrl"`
}

// Fragment is a piece of a message: type text | emote | mention. Never HTML.
type Fragment struct {
	Type   string    `json:"type"`
	Text   string    `json:"text"`
	Emote  *EmoteRef `json:"emote,omitempty"`
	UserID string    `json:"userId,omitempty"`
}

// Message has the same shape as the data of the WebSocket `message` event.
type Message struct {
	ID        string     `json:"id"`
	StreamID  string     `json:"streamId"`
	User      Author     `json:"user"`
	Body      string     `json:"body"`
	Fragments []Fragment `json:"fragments"`
	Type      string     `json:"type"`
	CreatedAt time.Time  `json:"createdAt"`
	Pinned    bool       `json:"pinned"`
	// Deleted messages keep their place with an empty body and no fragments.
	Deleted bool `json:"deleted"`
}

// MessagePage is newest first; nextCursor (a message id) is null on the last page.
type MessagePage struct {
	Items      []Message `json:"items"`
	NextCursor *string   `json:"nextCursor"`
}

// Emote -.
type Emote struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	ImageURL string `json:"imageUrl"`
	Animated bool   `json:"animated"`
	Scope    string `json:"scope"`
}

// EmoteList -.
type EmoteList struct {
	Items []Emote `json:"items"`
}

// ChatSettings -.
type ChatSettings struct {
	SlowSec      int        `json:"slowSec"`
	FollowersMin *int       `json:"followersMin"`
	SubsOnly     bool       `json:"subsOnly"`
	BlockLinks   bool       `json:"blockLinks"`
	UpdatedAt    *time.Time `json:"updatedAt"`
}

// UserRef -. AvatarURL is always null: accounts have no picture yet.
type UserRef struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// ActorRef -.
type ActorRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// Restriction is a ban (until null) or a running timeout.
type Restriction struct {
	User      UserRef    `json:"user"`
	Type      string     `json:"type" example:"TIMEOUT"`
	Until     *time.Time `json:"until"`
	Actor     ActorRef   `json:"actor"`
	Reason    *string    `json:"reason"`
	CreatedAt time.Time  `json:"createdAt"`
}

// RestrictionPage is newest first.
type RestrictionPage struct {
	Items      []Restriction `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

// RestrictionState -.
type RestrictionState struct {
	Type  string     `json:"type"`
	Until *time.Time `json:"until"`
}

// UserCard is a user seen from a channel's chat; restriction is null for callers who cannot moderate.
type UserCard struct {
	User        UserRef           `json:"user"`
	CreatedAt   *time.Time        `json:"createdAt"`
	Badges      []string          `json:"badges"`
	Role        *string           `json:"role" example:"MODERATOR"`
	FollowedAt  *time.Time        `json:"followedAt"`
	Restriction *RestrictionState `json:"restriction"`
}

// BlockedTerm -. Term is what the moderator typed.
type BlockedTerm struct {
	ID        string    `json:"id"`
	Term      string    `json:"term"`
	CreatedBy ActorRef  `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// BlockedTermList is newest first, in one page.
type BlockedTermList struct {
	Items      []BlockedTerm `json:"items"`
	NextCursor *string       `json:"nextCursor"`
	Limit      int           `json:"limit"`
}

// ChannelRole -.
type ChannelRole struct {
	User      UserRef   `json:"user"`
	Role      string    `json:"role" example:"MODERATOR"`
	GrantedBy ActorRef  `json:"grantedBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// RolePage is newest first.
type RolePage struct {
	Items      []ChannelRole `json:"items"`
	NextCursor *string       `json:"nextCursor"`
}

// ModChannelRef -.
type ModChannelRef struct {
	ID          string  `json:"id"`
	Handle      string  `json:"handle"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
	IsLive      bool    `json:"isLive"`
}

// ModChannel is a channel the caller moderates (or is VIP of).
type ModChannel struct {
	Channel ModChannelRef `json:"channel"`
	Role    string        `json:"role" example:"MODERATOR"`
}

// ModChannelList is live channels first, then by name, in one page.
type ModChannelList struct {
	Items      []ModChannel `json:"items"`
	NextCursor *string      `json:"nextCursor"`
}

// Pinned -.
type Pinned struct {
	Message Message `json:"message"`
}
