package v1

import (
	"time"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
)

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

// StreamKeyCreated is the only response that ever carries the key.
type StreamKeyCreated struct {
	StreamKey string `json:"streamKey"`
	RTMPURL   string `json:"rtmpUrl"`
}

// StreamKeyInfo -.
type StreamKeyInfo struct {
	HasKey    bool       `json:"hasKey"`
	RTMPURL   string     `json:"rtmpUrl"`
	CreatedAt *time.Time `json:"createdAt"`
}

// Settings -.
type Settings struct {
	Title     string     `json:"title"`
	Tags      []string   `json:"tags"`
	Language  string     `json:"language"`
	IsMature  bool       `json:"isMature"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

// Channel is the summary of a creator's channel shown next to streams and VODs.
type Channel struct {
	ID          string  `json:"id"`
	Handle      string  `json:"handle"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// Studio -.
type Studio struct {
	Channel  Channel       `json:"channel"`
	Settings Settings      `json:"settings"`
	Current  *StudioStream `json:"current"`
}

// StudioStream -.
type StudioStream struct {
	StreamID      string         `json:"streamId"`
	Status        string         `json:"status"`
	StartedAt     *time.Time     `json:"startedAt"`
	InterruptedAt *time.Time     `json:"interruptedAt"`
	ManifestURL   string         `json:"manifestUrl"`
	Health        *entity.Health `json:"health"`
}

// Stream -.
type Stream struct {
	ID           string     `json:"id"`
	ChannelID    string     `json:"channelId"`
	Title        string     `json:"title"`
	Tags         []string   `json:"tags"`
	Language     string     `json:"language"`
	IsMature     bool       `json:"isMature"`
	Status       string     `json:"status"`
	StartedAt    *time.Time `json:"startedAt"`
	EndedAt      *time.Time `json:"endedAt"`
	EndReason    *string    `json:"endReason"`
	ManifestURL  string     `json:"manifestUrl"`
	ThumbnailURL string     `json:"thumbnailUrl"`
	VODID        *string    `json:"vodId"`
	Channel      *Channel   `json:"channel"`
}

// StreamList -.
type StreamList struct {
	Items      []Stream `json:"items"`
	NextCursor *string  `json:"nextCursor"`
}

// VOD -.
type VOD struct {
	ID           string    `json:"id"`
	StreamID     string    `json:"streamId"`
	ChannelID    string    `json:"channelId"`
	Title        string    `json:"title"`
	DurationSec  int       `json:"durationSec"`
	ManifestURL  string    `json:"manifestUrl"`
	ThumbnailURL string    `json:"thumbnailUrl"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Channel      *Channel  `json:"channel"`
}
