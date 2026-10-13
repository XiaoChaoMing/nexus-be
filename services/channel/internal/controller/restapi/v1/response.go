package v1

import "time"

// Data wraps every successful body.
type Data[T any] struct {
	Data T `json:"data"`
}

// ErrorResponse wraps every error body.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody -.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// Channel is the public channel page.
type Channel struct {
	ID              string    `json:"id"`
	Handle          string    `json:"handle"`
	DisplayName     string    `json:"displayName"`
	Bio             *string   `json:"bio"`
	AvatarURL       *string   `json:"avatarUrl"`
	IsLive          bool      `json:"isLive"`
	CurrentStreamID *string   `json:"currentStreamId"`
	CreatedAt       time.Time `json:"createdAt"`
}
