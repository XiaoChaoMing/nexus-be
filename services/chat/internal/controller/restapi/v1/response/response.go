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
	UpdatedAt    *time.Time `json:"updatedAt"`
}
