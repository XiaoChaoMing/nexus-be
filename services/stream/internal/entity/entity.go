// Package entity holds stream-svc domain types (ERD `stream`, NXL-A §7A).
package entity

import (
	"errors"
	"time"
)

// Status of a stream; only the transitions of NXL-A §7A are allowed.
type Status string

// Statuses.
const (
	StatusLive        Status = "LIVE"
	StatusInterrupted Status = "INTERRUPTED"
	StatusEnded       Status = "ENDED"
)

// EndReason -.
type EndReason string

// End reasons.
const (
	EndCreator       EndReason = "creator"
	EndIngestTimeout EndReason = "ingest_timeout"
)

// Stream is one broadcast.
type Stream struct {
	ID            string
	ChannelID     string
	Title         string
	Tags          []string
	Language      string
	IsMature      bool
	Status        Status
	StartedAt     *time.Time
	InterruptedAt *time.Time
	EndedAt       *time.Time
	EndReason     *EndReason
	CreatedAt     time.Time
	VODID         *string
	// Channel is nil when channel.created never arrived for ChannelID (streams before channels existed).
	Channel *Channel
}

// Channel is stream-svc's copy of a creator's channel (creator_channels, fed by channel.created).
type Channel struct {
	ID          string
	OwnerID     string
	Handle      string
	DisplayName string
	AvatarURL   *string
}

// OnAir reports whether viewers can watch it live.
func (s Stream) OnAir() bool {
	return s.Status == StatusLive || s.Status == StatusInterrupted
}

// Settings are the creator's values for the next (or current) broadcast.
type Settings struct {
	ChannelID string
	Title     string
	Tags      []string
	Language  string
	IsMature  bool
	UpdatedAt time.Time
}

// DefaultTitle is used when the creator never set one (streams.title is NOT NULL).
const DefaultTitle = "Buổi phát trực tiếp"

// StreamKeyInfo never contains the key itself: only its hash is stored.
type StreamKeyInfo struct {
	HasKey    bool
	CreatedAt *time.Time
}

// VOD -.
type VOD struct {
	ID           string
	StreamID     string
	ChannelID    string
	Title        string
	DurationSec  int
	PlaylistPath string
	ThumbnailURL string
	Status       string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	Channel      *Channel
}

// Grant is the answer to media-svc's authorize call.
type Grant struct {
	StreamID  string
	ChannelID string
	Resumed   bool
}

// Health is the latest ingest.health report of a live stream (memory only).
type Health struct {
	FPS          float64   `json:"fps"`
	BitrateKbps  float64   `json:"bitrateKbps"`
	DropFrames   int64     `json:"droppedFrames"`
	Speed        float64   `json:"speed"`
	UploadErrors int64     `json:"uploadErrors"`
	ReportedAt   time.Time `json:"reportedAt"`
}

var (
	// ErrInvalidStreamKey -.
	ErrInvalidStreamKey = errors.New("invalid stream key")
	// ErrAlreadyLive -.
	ErrAlreadyLive = errors.New("channel already live")
	// ErrNotLive -.
	ErrNotLive = errors.New("channel is not live")
	// ErrNotFound -.
	ErrNotFound = errors.New("not found")
	// ErrRecentlyEnded refuses a publish right after the creator ended the stream, so OBS auto-reconnect
	// does not start a new broadcast.
	ErrRecentlyEnded = errors.New("channel ended a stream moments ago")
	// ErrNotCreator: the caller owns no channel, so the Studio is closed to them.
	ErrNotCreator = errors.New("caller has no channel")
)
