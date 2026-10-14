// Package events is the contract of NexusLive domain events (subject = "nxl." + type).
// Payloads are JSON; the NATS message id is the event id.
package events

import "time"

// Event types.
const (
	// Published by media-svc.
	TypeIngestStarted = "ingest.started.v1"
	TypeIngestStopped = "ingest.stopped.v1"
	TypeIngestHealth  = "ingest.health.v1"
	TypeVODProcessed  = "vod.processed.v1"

	// Published by stream-svc (outbox).
	TypeStreamStarted       = "stream.started.v1"
	TypeStreamEnded         = "stream.ended.v1"
	TypeStreamStatusChanged = "stream.status_changed.v1"
	TypeStreamUpdated       = "stream.updated.v1"
	TypeStreamVODReady      = "stream.vod_ready.v1"
	TypeStreamScheduled     = "stream.scheduled.v1"
	TypeVODUpdated          = "vod.updated.v1"
	TypeVODDeleted          = "vod.deleted.v1"

	// Published by chat-svc (outbox).
	TypeStreamPeakViewers = "stream.peak_viewers.v1"

	// Published by identity-svc (outbox).
	TypeUserRegistered         = "user.registered.v1"
	TypeEmailRequested         = "auth.email_requested.v1"
	TypeUserEmailVerified      = "user.email_verified.v1"
	TypeUserProfileUpdated     = "user.profile_updated.v1"
	TypeCreatorApplicationSent = "creator.application_submitted.v1"
	TypeCreatorApproved        = "creator.approved.v1"
	TypeCreatorRejected        = "creator.rejected.v1"

	// Published by channel-svc (outbox).
	TypeChannelCreated             = "channel.created.v1"
	TypeChannelUpdated             = "channel.updated.v1"
	TypeFollowCreated              = "follow.created.v1"
	TypeFollowUpdated              = "follow.updated.v1"
	TypeFollowDeleted              = "follow.deleted.v1"
	TypeScheduleCreated            = "schedule.created.v1"
	TypeScheduleUpdated            = "schedule.updated.v1"
	TypeScheduleOccurrenceUpcoming = "schedule.occurrence_upcoming.v1"
	TypeScheduleOccurrenceChanged  = "schedule.occurrence_changed.v1"
	TypeScheduleReminderDue        = "schedule.reminder_due.v1"
)

// Notification levels of a follow (follows.notify_level).
const (
	NotifyAll      = "ALL"
	NotifyLiveOnly = "LIVE_ONLY"
	NotifyOff      = "OFF"
)

// Reasons of ScheduleOccurrenceChanged.
const (
	OccurrenceCancelled     = "CANCELLED"
	OccurrenceMoved         = "MOVED"
	OccurrenceSeriesChanged = "SERIES_CHANGED"
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

// StreamStarted -. Fields after Title were added later: consumers treat their zero values as unknown.
type StreamStarted struct {
	StreamID   string    `json:"streamId"`
	ChannelID  string    `json:"channelId"`
	Title      string    `json:"title"`
	CategoryID *string   `json:"categoryId"`
	Tags       []string  `json:"tags"`
	Language   string    `json:"language"`
	IsMature   bool      `json:"isMature"`
	StartedAt  time.Time `json:"startedAt"`
	ScheduleID *string   `json:"scheduleId"`
}

// StreamEnded tells media-svc to kick the publisher and finalize the VOD.
type StreamEnded struct {
	StreamID  string `json:"streamId"`
	ChannelID string `json:"channelId"`
	EndReason string `json:"endReason"`
}

// Stream statuses carried by StreamStatusChanged.
const (
	StreamStatusLive        = "LIVE"
	StreamStatusInterrupted = "INTERRUPTED"
	StreamStatusScheduled   = "SCHEDULED"
	StreamStatusCancelled   = "CANCELLED"
)

// StreamStatusChanged is published on LIVE → INTERRUPTED and INTERRUPTED → LIVE (ENDED has stream.ended),
// SCHEDULED → CANCELLED, and SCHEDULED with a new ScheduledStartAt when an occurrence is moved.
type StreamStatusChanged struct {
	StreamID         string     `json:"streamId"`
	ChannelID        string     `json:"channelId"`
	Status           string     `json:"status"`
	OccurredAt       time.Time  `json:"occurredAt"`
	ScheduledStartAt *time.Time `json:"scheduledStartAt"`
}

// StreamUpdated is published when the creator changes the settings of the stream on air.
type StreamUpdated struct {
	StreamID   string   `json:"streamId"`
	ChannelID  string   `json:"channelId"`
	Title      string   `json:"title"`
	Tags       []string `json:"tags"`
	CategoryID *string  `json:"categoryId"`
	Language   string   `json:"language"`
	IsMature   bool     `json:"isMature"`
}

// StreamVODReady is published when the stream's VOD becomes READY.
type StreamVODReady struct {
	StreamID     string    `json:"streamId"`
	ChannelID    string    `json:"channelId"`
	VODID        string    `json:"vodId"`
	Title        string    `json:"title"`
	ThumbnailURL string    `json:"thumbnailUrl"`
	DurationSec  int       `json:"durationSec"`
	CategoryID   *string   `json:"categoryId"`
	IsMature     bool      `json:"isMature"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// StreamScheduled is published when stream-svc creates the SCHEDULED stream of an occurrence, or moves it.
type StreamScheduled struct {
	StreamID         string    `json:"streamId"`
	ChannelID        string    `json:"channelId"`
	ScheduleID       string    `json:"scheduleId"`
	ScheduledStartAt time.Time `json:"scheduledStartAt"`
}

// VODUpdated is published when the creator renames, hides or shows a VOD.
type VODUpdated struct {
	VODID        string    `json:"vodId"`
	StreamID     string    `json:"streamId"`
	ChannelID    string    `json:"channelId"`
	Title        string    `json:"title"`
	Visibility   string    `json:"visibility"`
	Status       string    `json:"status"`
	ThumbnailURL string    `json:"thumbnailUrl"`
	DurationSec  int       `json:"durationSec"`
	CategoryID   *string   `json:"categoryId"`
	IsMature     bool      `json:"isMature"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// VODDeleted is published when the creator deletes a VOD; media-svc removes its objects.
type VODDeleted struct {
	VODID     string `json:"vodId"`
	StreamID  string `json:"streamId"`
	ChannelID string `json:"channelId"`
}

// StreamPeakViewers carries the highest viewer count chat-svc measured, sent after stream.ended.
type StreamPeakViewers struct {
	StreamID    string `json:"streamId"`
	PeakViewers int    `json:"peakViewers"`
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

// ChannelUpdated is published when a channel's display name, handle or avatar changes.
type ChannelUpdated struct {
	ChannelID   string  `json:"channelId"`
	OwnerID     string  `json:"ownerId"`
	Handle      string  `json:"handle"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// FollowCreated -. NotifyLevel is empty in events published before it existed (= ALL).
type FollowCreated struct {
	UserID      string    `json:"userId"`
	ChannelID   string    `json:"channelId"`
	CreatedAt   time.Time `json:"createdAt"`
	NotifyLevel string    `json:"notifyLevel"`
}

// FollowUpdated is published when the follower changes the notification level.
type FollowUpdated struct {
	UserID      string    `json:"userId"`
	ChannelID   string    `json:"channelId"`
	NotifyLevel string    `json:"notifyLevel"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// FollowDeleted -.
type FollowDeleted struct {
	UserID    string `json:"userId"`
	ChannelID string `json:"channelId"`
}

// ScheduleCreated -.
type ScheduleCreated struct {
	ScheduleID string    `json:"scheduleId"`
	ChannelID  string    `json:"channelId"`
	Title      string    `json:"title"`
	StartAt    time.Time `json:"startAt"`
}

// ScheduleUpdated is published when only the title, category or duration of a schedule change.
type ScheduleUpdated struct {
	ScheduleID  string  `json:"scheduleId"`
	ChannelID   string  `json:"channelId"`
	Title       string  `json:"title"`
	CategoryID  *string `json:"categoryId"`
	DurationMin int     `json:"durationMin"`
}

// ScheduleOccurrenceUpcoming announces an occurrence starting within the upcoming lead (24 h).
type ScheduleOccurrenceUpcoming struct {
	ScheduleID   string    `json:"scheduleId"`
	ChannelID    string    `json:"channelId"`
	Title        string    `json:"title"`
	OccurrenceAt time.Time `json:"occurrenceAt"`
	CategoryID   *string   `json:"categoryId"`
	DurationMin  int       `json:"durationMin"`
}

// ScheduleOccurrenceChanged: an occurrence was cancelled, moved (NewScheduleID, NewOccurrenceAt) or its
// series changed. UserIDs are the viewers who had a reminder for it.
type ScheduleOccurrenceChanged struct {
	ScheduleID      string     `json:"scheduleId"`
	ChannelID       string     `json:"channelId"`
	Title           string     `json:"title"`
	OccurrenceAt    time.Time  `json:"occurrenceAt"`
	NewScheduleID   *string    `json:"newScheduleId"`
	NewOccurrenceAt *time.Time `json:"newOccurrenceAt"`
	Reason          string     `json:"reason"`
	UserIDs         []string   `json:"userIds"`
}

// ScheduleReminderDue lists viewers to remind of an occurrence (at most 500 per event).
type ScheduleReminderDue struct {
	ScheduleID   string    `json:"scheduleId"`
	ChannelID    string    `json:"channelId"`
	Title        string    `json:"title"`
	OccurrenceAt time.Time `json:"occurrenceAt"`
	UserIDs      []string  `json:"userIds"`
}
