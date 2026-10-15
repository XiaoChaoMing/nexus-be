// Package events is the contract of NexusLive domain events (subject = "nxl." + type).
// Payloads are JSON; the NATS message id is the event id.
package events

import (
	"encoding/json"
	"time"
)

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
	// Published by channel-svc (outbox) when safety suspends or restores a channel.
	TypeChannelSuspended = "channel.suspended.v1"
	TypeChannelRestored  = "channel.restored.v1"

	// Published by chat-svc (outbox): moderation, chat modes, mentions, channel roles.
	TypeChatModAction       = "chat.mod_action.v1"
	TypeChatSettingsChanged = "chat.settings_changed.v1"
	TypeChatMention         = "chat.mention.v1"
	TypeChannelRoleChanged  = "channel.role_changed.v1"

	// Published by identity-svc and chat-svc for actions that have no domain event; safety-svc stores them.
	TypeAuditRecorded = "audit.recorded.v1"

	// Published by safety-svc (outbox); the owning service applies the action.
	TypeReportResolved  = "safety.report_resolved.v1"
	TypeUserActioned    = "safety.user_actioned.v1"
	TypeChannelActioned = "safety.channel_actioned.v1"
	TypeStreamTakedown  = "safety.stream_takedown.v1"

	// Published by identity-svc (outbox).
	TypeUserLocked    = "user.locked.v1"
	TypeUserUnlocked  = "user.unlocked.v1"
	TypeUserBlocked   = "user.blocked.v1"
	TypeUserUnblocked = "user.unblocked.v1"

	// Published by notification-svc (outbox); chat-svc relays them on user:{userId}.
	TypeNotificationCreated       = "notification.created.v1"
	TypeNotificationUnreadChanged = "notification.unread_changed.v1"
)

// EndReasonAdminTakedown ends a stream an admin took down (stream.ended.v1 endReason).
const EndReasonAdminTakedown = "admin_takedown"

// Types of ChatModAction.
const (
	ModDelete    = "DELETE"
	ModTimeout   = "TIMEOUT"
	ModUntimeout = "UNTIMEOUT"
	ModBan       = "BAN"
	ModUnban     = "UNBAN"
	ModPin       = "PIN"
	ModUnpin     = "UNPIN"
)

// Actor roles of moderation and audit events.
const (
	ActorOwner     = "OWNER"
	ActorModerator = "MODERATOR"
	ActorAdmin     = "ADMIN"
	ActorUser      = "USER"
	ActorSystem    = "SYSTEM"
)

// Actions of UserActioned and ChannelActioned.
const (
	ActionLock    = "LOCK"
	ActionUnlock  = "UNLOCK"
	ActionSuspend = "SUSPEND"
	ActionRestore = "RESTORE"
)

// Final statuses of ReportResolved.
const (
	ReportStatusResolved  = "RESOLVED"
	ReportStatusDismissed = "DISMISSED"
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

// UserRegistered -. Locale and Timezone are empty in events published before they existed: consumers use
// "vi" and "Asia/Ho_Chi_Minh".
type UserRegistered struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
}

// Email purposes of EmailRequested.
const (
	EmailPurposeVerify = "VERIFY"
	EmailPurposeReset  = "RESET"
	// EmailPurposeSecurityLock tells the user their account was locked after failed sign-ins (Token is empty).
	EmailPurposeSecurityLock = "SECURITY_LOCK"
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

// UserProfileUpdated carries the current display name, locale and time zone (the last two are empty in
// events published before they existed).
type UserProfileUpdated struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
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

// ChatModAction is one row of chat-svc's mod_actions; MessageBody is the original text of a deleted or
// pinned message (audit only, never shown to viewers).
type ChatModAction struct {
	ActionID     string     `json:"actionId"`
	ChannelID    string     `json:"channelId"`
	StreamID     *string    `json:"streamId"`
	ActorID      string     `json:"actorId"`
	ActorRole    string     `json:"actorRole"`
	Type         string     `json:"type"`
	TargetUserID *string    `json:"targetUserId"`
	MessageID    *string    `json:"messageId"`
	MessageBody  *string    `json:"messageBody"`
	Reason       *string    `json:"reason"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	OccurredAt   time.Time  `json:"occurredAt"`
}

// ChatModes are a channel's chat modes in ChatSettingsChanged.
type ChatModes struct {
	SlowSec      int  `json:"slowSec"`
	FollowersMin *int `json:"followersMin"`
	SubsOnly     bool `json:"subsOnly"`
	BlockLinks   bool `json:"blockLinks"`
}

// ChatSettingsChanged is published when a channel's chat modes change.
type ChatSettingsChanged struct {
	ChannelID string    `json:"channelId"`
	ActorID   string    `json:"actorId"`
	ActorRole string    `json:"actorRole"`
	Before    ChatModes `json:"before"`
	After     ChatModes `json:"after"`
}

// ChatMention lists the users a message mentions (at most 5, never the sender).
type ChatMention struct {
	MessageID        string   `json:"messageId"`
	StreamID         string   `json:"streamId"`
	ChannelID        string   `json:"channelId"`
	SenderID         string   `json:"senderId"`
	SenderUsername   string   `json:"senderUsername"`
	MentionedUserIDs []string `json:"mentionedUserIds"`
	// Excerpt is plain text, at most 100 characters.
	Excerpt string `json:"excerpt"`
}

// ChannelRoleChanged is published when a moderator or VIP role is granted, changed (Role and PreviousRole
// set) or revoked (Role nil).
type ChannelRoleChanged struct {
	ChannelID    string  `json:"channelId"`
	UserID       string  `json:"userId"`
	Role         *string `json:"role"`
	PreviousRole *string `json:"previousRole"`
	ActorID      string  `json:"actorId"`
	ActorRole    string  `json:"actorRole"`
}

// AuditRecorded is an action to keep in the audit log that has no domain event of its own.
type AuditRecorded struct {
	ActorID      *string        `json:"actorId"`
	ActorRole    string         `json:"actorRole"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   *string        `json:"resourceId"`
	Reason       *string        `json:"reason"`
	Metadata     map[string]any `json:"metadata"`
	RequestID    *string        `json:"requestId"`
}

// ReportResolved is published for every report an admin closes.
type ReportResolved struct {
	ReportID   string `json:"reportId"`
	ReporterID string `json:"reporterId"`
	TargetType string `json:"targetType"`
	Status     string `json:"status"`
}

// UserActioned asks identity-svc to lock (LockedUntil nil = permanently) or unlock an account.
type UserActioned struct {
	ActionID    string     `json:"actionId"`
	UserID      string     `json:"userId"`
	Action      string     `json:"action"`
	LockedUntil *time.Time `json:"lockedUntil"`
	Reason      string     `json:"reason"`
	AdminID     string     `json:"adminId"`
}

// ChannelActioned asks channel-svc to suspend or restore a channel.
type ChannelActioned struct {
	ActionID  string `json:"actionId"`
	ChannelID string `json:"channelId"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
	AdminID   string `json:"adminId"`
}

// StreamTakedown asks stream-svc to end a stream and delete its VOD.
type StreamTakedown struct {
	ActionID  string `json:"actionId"`
	StreamID  string `json:"streamId"`
	ChannelID string `json:"channelId"`
	Title     string `json:"title"`
	Reason    string `json:"reason"`
	AdminID   string `json:"adminId"`
}

// UserLocked is published when an admin locks an account (LockedUntil nil = permanently); a temporary
// lock after failed sign-ins is not announced.
type UserLocked struct {
	UserID      string     `json:"userId"`
	LockedUntil *time.Time `json:"lockedUntil"`
	Reason      string     `json:"reason"`
	ActionID    *string    `json:"actionId"`
}

// UserUnlocked is published when an admin lifts a lock.
type UserUnlocked struct {
	UserID   string  `json:"userId"`
	ActionID *string `json:"actionId"`
}

// UserBlocked is the payload of user.blocked.v1 and user.unblocked.v1.
type UserBlocked struct {
	BlockerID string `json:"blockerId"`
	BlockedID string `json:"blockedId"`
}

// ChannelSuspended is the payload of channel.suspended.v1 and channel.restored.v1.
type ChannelSuspended struct {
	ChannelID string  `json:"channelId"`
	OwnerID   string  `json:"ownerId"`
	Reason    string  `json:"reason"`
	ActionID  *string `json:"actionId"`
}

// NotificationCreated is published when a notification is created or grouped into an existing one;
// UnreadCount is the recipient's unread count after the change.
type NotificationCreated struct {
	NotificationID string          `json:"notificationId"`
	UserID         string          `json:"userId"`
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	Link           *string         `json:"link"`
	GroupCount     int             `json:"groupCount"`
	Read           bool            `json:"read"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	UnreadCount    int             `json:"unreadCount"`
}

// NotificationUnreadChanged is published when reading or deleting notifications changes the unread count.
type NotificationUnreadChanged struct {
	UserID      string `json:"userId"`
	UnreadCount int    `json:"unreadCount"`
}
