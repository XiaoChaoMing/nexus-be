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
	// StatusScheduled is an announced occurrence of a schedule (NXL-G2): Go Live inside its window attaches to it.
	StatusScheduled Status = "SCHEDULED"
	StatusCancelled Status = "CANCELLED"
)

// EndReason -.
type EndReason string

// End reasons.
const (
	EndCreator       EndReason = "creator"
	EndIngestTimeout EndReason = "ingest_timeout"
	// EndAdminTakedown: an admin took the stream down or suspended its channel.
	EndAdminTakedown EndReason = "admin_takedown"
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
	// VODVisibility is the visibility of the READY VOD (nil without one).
	VODVisibility    *string
	CategoryID       *string
	ScheduleID       *string
	ScheduledStartAt *time.Time
	// ViewerCount is nil when chat-svc has no count for the stream (or counts are not configured).
	ViewerCount *int
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
	// Suspended: safety-svc suspended the channel (channel.suspended.v1).
	Suspended bool
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
	// CategoryID is a channel-svc category; stream-svc does not know the list and stores any uuid.
	CategoryID *string
	UpdatedAt  time.Time
}

// DefaultTitle is used when the creator never set one (streams.title is NOT NULL).
const DefaultTitle = "Buổi phát trực tiếp"

// StreamKeyInfo never contains the key itself: only its hash is stored.
type StreamKeyInfo struct {
	HasKey    bool
	CreatedAt *time.Time
}

// VOD visibilities and statuses. A LIVE VOD goes READY → EXPIRED; an UPLOAD VOD goes PROCESSING → READY | FAILED and
// FAILED → PROCESSING (retry). Both end DELETED.
const (
	VisibilityPublic = "PUBLIC"
	VisibilityHidden = "HIDDEN"
	VODProcessing    = "PROCESSING"
	VODReady         = "READY"
	VODExpired       = "EXPIRED"
	VODDeleted       = "DELETED"
	VODFailed        = "FAILED"
)

// VOD sources: the recording of a broadcast or a file the creator uploaded.
const (
	SourceLive   = "LIVE"
	SourceUpload = "UPLOAD"
)

// VOD -.
type VOD struct {
	ID string
	// StreamID is nil for an uploaded video.
	StreamID     *string
	ChannelID    string
	Source       string
	Title        string
	Description  string
	DurationSec  int
	PlaylistPath string
	ThumbnailURL string
	Status       string
	Visibility   string
	CategoryID   *string
	IsMature     bool
	CreatedAt    time.Time
	// PublishedAt is when the VOD became READY (nil while PROCESSING or FAILED).
	PublishedAt *time.Time
	// ExpiresAt is nil for an uploaded video: it never expires.
	ExpiresAt *time.Time
	// FailureReason and FailureRetryable describe a FAILED upload.
	FailureReason    *string
	FailureRetryable bool
	// ProcessingPercent and ProcessingStage report the transcoding of a PROCESSING upload.
	ProcessingPercent int
	ProcessingStage   *string
	Channel           *Channel
}

// Hidden reports whether only the owner may see the VOD (BR-G2-07).
func (v VOD) Hidden() bool { return v.Visibility == VisibilityHidden }

// Uploaded reports whether the creator uploaded the video (no broadcast behind it).
func (v VOD) Uploaded() bool { return v.Source == SourceUpload }

// PlaylistPrefix is the object prefix media-svc removes when the VOD is deleted.
func (v VOD) PlaylistPrefix() string {
	if v.Uploaded() || v.StreamID == nil {
		return "videos/" + v.ID + "/"
	}

	return "streams/" + *v.StreamID + "/"
}

// CanApply checks a creator's change against the VOD's source and state: the description, category and mature flag
// belong to uploaded videos; a LIVE VOD changes while READY, an uploaded one while READY or PROCESSING.
func (v VOD) CanApply(p VODPatch) error {
	if !v.Uploaded() {
		if p.Description != nil || p.CategoryID != nil || p.IsMature != nil || v.Status != VODReady {
			return ErrInvalidState
		}

		return nil
	}

	if v.Status != VODReady && v.Status != VODProcessing {
		return ErrInvalidState
	}

	return nil
}

// Retryable reports whether the creator may transcode a FAILED upload again.
func (v VOD) Retryable() bool {
	return v.Uploaded() && v.Status == VODFailed && v.FailureRetryable
}

// VODPatch is the creator's change to a VOD; nil fields stay unchanged. CategoryID pointing to "" removes the
// category.
type VODPatch struct {
	Title       *string
	Visibility  *string
	Description *string
	CategoryID  *string
	IsMature    *bool
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
	// ErrInvalidState: the transition is not allowed in the current state (an EXPIRED VOD cannot be edited).
	ErrInvalidState = errors.New("invalid state")
	// ErrChannelSuspended: the channel is suspended (no publish, playback or Studio change).
	ErrChannelSuspended = errors.New("channel suspended")
	// ErrInvalidCursor: a snapshot cursor that this service never returned.
	ErrInvalidCursor = errors.New("invalid cursor")
)

// TakedownResult says what an admin takedown changed.
type TakedownResult struct {
	// Ended is true when the stream was on air and is now ENDED (admin_takedown).
	Ended bool
	// VODsDeleted counts the VODs moved to DELETED.
	VODsDeleted int
	// Found is false when the stream does not exist.
	Found bool
}

// StreamCopy is what other services copy of a stream (internal snapshot).
type StreamCopy struct {
	ID         string
	ChannelID  string
	Title      string
	Status     Status
	EndReason  *string
	VODID      *string
	VODDeleted bool
	StartedAt  *time.Time
	EndedAt    *time.Time
}
