// Package events is the contract of NexusLive domain events (subject = "nxl." + type).
// Payloads are JSON; the NATS message id is the event id.
package events

// Event types.
const (
	// Published by media-svc.
	TypeIngestStarted = "ingest.started.v1"
	TypeIngestStopped = "ingest.stopped.v1"
	TypeIngestHealth  = "ingest.health.v1"
	TypeVODProcessed  = "vod.processed.v1"

	// Published by stream-svc (outbox).
	TypeStreamStarted = "stream.started.v1"
	TypeStreamEnded   = "stream.ended.v1"

	// Published by identity-svc (outbox).
	TypeUserRegistered         = "user.registered.v1"
	TypeEmailRequested         = "auth.email_requested.v1"
	TypeUserEmailVerified      = "user.email_verified.v1"
	TypeUserProfileUpdated     = "user.profile_updated.v1"
	TypeCreatorApplicationSent = "creator.application_submitted.v1"
	TypeCreatorApproved        = "creator.approved.v1"
	TypeCreatorRejected        = "creator.rejected.v1"

	// Published by channel-svc (outbox).
	TypeChannelCreated = "channel.created.v1"
)

// IngestStarted -.
type IngestStarted struct {
	StreamID     string   `json:"streamId"`
	ChannelID    string   `json:"channelId"`
	Renditions   []string `json:"renditions"`
	SourceWidth  int      `json:"sourceWidth"`
	SourceHeight int      `json:"sourceHeight"`
	Resumed      bool     `json:"resumed"`
}

// Ingest stop reasons.
const (
	ReasonPublisherDisconnected = "publisher_disconnected"
	ReasonTranscoderFailed      = "transcoder_failed"
	ReasonKicked                = "kicked"
	ReasonShutdown              = "shutdown"
	// ReasonRecovered: media-svc found the stream's files after a crash and closed it on startup.
	ReasonRecovered = "recovered"
)

// IngestStopped -.
type IngestStopped struct {
	StreamID string `json:"streamId"`
	Reason   string `json:"reason"`
}

// IngestHealth is sent every 5 s while a stream transcodes.
type IngestHealth struct {
	StreamID      string  `json:"streamId"`
	FPS           float64 `json:"fps"`
	BitrateKbps   float64 `json:"bitrateKbps"`
	DroppedFrames int64   `json:"droppedFrames"`
	Speed         float64 `json:"speed"`
	UploadErrors  int64   `json:"uploadErrors"`
}

// VODProcessed -.
type VODProcessed struct {
	StreamID      string  `json:"streamId"`
	DurationSec   float64 `json:"durationSec"`
	PlaylistPath  string  `json:"playlistPath"`  // object key of master.m3u8
	ThumbnailPath string  `json:"thumbnailPath"` // object key of thumb.jpg
}

// StreamStarted -.
type StreamStarted struct {
	StreamID  string `json:"streamId"`
	ChannelID string `json:"channelId"`
	Title     string `json:"title"`
}

// StreamEnded tells media-svc to kick the publisher and finalize the VOD.
type StreamEnded struct {
	StreamID  string `json:"streamId"`
	ChannelID string `json:"channelId"`
	EndReason string `json:"endReason"`
}

// UserRegistered -.
type UserRegistered struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

// Email purposes of EmailRequested.
const (
	EmailPurposeVerify = "VERIFY"
	EmailPurposeReset  = "RESET"
)

// EmailRequested asks for a verification or password-reset e-mail. Token is the raw one-time token:
// this event only travels on the internal bus and must never be logged.
type EmailRequested struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Purpose  string `json:"purpose"`
	Token    string `json:"token"`
	Locale   string `json:"locale"`
}

// UserEmailVerified -.
type UserEmailVerified struct {
	UserID string `json:"userId"`
}

// UserProfileUpdated -.
type UserProfileUpdated struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
}

// CreatorApplicationSubmitted -.
type CreatorApplicationSubmitted struct {
	ApplicationID string `json:"applicationId"`
	UserID        string `json:"userId"`
}

// CreatorApproved makes channel-svc create the creator's channel.
type CreatorApproved struct {
	ApplicationID string `json:"applicationId"`
	UserID        string `json:"userId"`
	Username      string `json:"username"`
	DisplayName   string `json:"displayName"`
}

// CreatorRejected -.
type CreatorRejected struct {
	ApplicationID string `json:"applicationId"`
	UserID        string `json:"userId"`
	Reason        string `json:"reason"`
}

// ChannelCreated -.
type ChannelCreated struct {
	ChannelID   string `json:"channelId"`
	OwnerID     string `json:"ownerId"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}
