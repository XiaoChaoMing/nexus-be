// Package entity holds safety-svc domain types (ERD safety, NXL-F §7).
package entity

import (
	"encoding/json"
	"time"
	"unicode/utf8"
)

// Report target types.
const (
	TargetUser    = "USER"
	TargetChannel = "CHANNEL"
	TargetStream  = "STREAM"
	TargetMessage = "MESSAGE"
)

// Report reasons.
const (
	ReasonSpam       = "SPAM"
	ReasonHarassment = "HARASSMENT"
	ReasonViolence   = "VIOLENCE"
	ReasonSensitive  = "SENSITIVE"
	ReasonCopyright  = "COPYRIGHT"
	ReasonOther      = "OTHER"
)

// Report statuses.
const (
	ReportOpen      = "OPEN"
	ReportInReview  = "IN_REVIEW"
	ReportResolved  = "RESOLVED"
	ReportDismissed = "DISMISSED"
)

// Evidence statuses.
const (
	EvidenceCaptured    = "CAPTURED"
	EvidenceNone        = "NONE"
	EvidenceUnavailable = "UNAVAILABLE"
)

// Admin action types.
const (
	ActionLockUser       = "LOCK_USER"
	ActionUnlockUser     = "UNLOCK_USER"
	ActionSuspendChannel = "SUSPEND_CHANNEL"
	ActionRestoreChannel = "RESTORE_CHANNEL"
	ActionTakedownStream = "TAKEDOWN_STREAM"
)

// Statuses of the copies.
const (
	UserActive       = "ACTIVE"
	UserLocked       = "LOCKED"
	UserDeleted      = "DELETED"
	RoleAdmin        = "ADMIN"
	RoleUser         = "USER"
	ChannelActive    = "ACTIVE"
	ChannelSuspended = "SUSPENDED"
	StreamScheduled  = "SCHEDULED"
	StreamLive       = "LIVE"
	StreamInterrupt  = "INTERRUPTED"
	StreamEnded      = "ENDED"
	StreamCancelled  = "CANCELLED"
)

// Limits of NXL-F (NXL-00 §12).
const (
	ReportNoteMax   = 500
	EvidenceCount   = 10
	AdminReasonMin  = 10
	AdminReasonMax  = 500
	ResolveNoteMin  = 10
	ResolveNoteMax  = 500
	LabelExcerptMax = 80
)

// Valid reports reasons and targets.
func validOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}

	return false
}

// ValidTarget -.
func ValidTarget(t string) bool {
	return validOf(t, TargetUser, TargetChannel, TargetStream, TargetMessage)
}

// ValidReason -.
func ValidReason(r string) bool {
	return validOf(r, ReasonSpam, ReasonHarassment, ReasonViolence, ReasonSensitive, ReasonCopyright, ReasonOther)
}

// ValidStatus -.
func ValidStatus(s string) bool {
	return validOf(s, ReportOpen, ReportInReview, ReportResolved, ReportDismissed)
}

// EvidenceItem is a chat message captured when a report is filed.
type EvidenceItem struct {
	MessageID string          `json:"messageId"`
	UserID    string          `json:"userId"`
	Body      string          `json:"body"`
	Fragments json.RawMessage `json:"fragments"`
	CreatedAt time.Time       `json:"createdAt"`
	Deleted   bool            `json:"deleted"`
}

// Ref names a user in lists.
type Ref struct {
	ID       string
	Username string
}

// Report is one row of reports.
type Report struct {
	ID             string
	ReporterID     string
	Reporter       Ref
	TargetType     string
	TargetID       string
	TargetUserID   *string
	StreamID       *string
	Reason         string
	Note           *string
	Evidence       []EvidenceItem
	EvidenceStatus string
	Status         string
	AssignedTo     *Ref
	ResolvedBy     *Ref
	ResolvedAt     *time.Time
	ResolutionNote *string
	SameTarget     int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Open reports whether an admin still has to handle the report.
func (r Report) Open() bool { return r.Status == ReportOpen || r.Status == ReportInReview }

// Target is a report's target as the admin app shows it.
type Target struct {
	ID     string
	Label  string
	Link   *string
	UserID *string
}

// ReportView is a report with its target label.
type ReportView struct {
	Report
	Target Target
}

// ReportDetail is GET /admin/reports/{id}.
type ReportDetail struct {
	ReportView
	Related []ReportView
	Actions []AdminAction
}

// AdminAction is one row of admin_actions.
type AdminAction struct {
	ID         string
	AdminID    string
	Admin      Ref
	ReportID   *string
	Type       string
	TargetType string
	TargetID   string
	Reason     string
	ExpiresAt  *time.Time
	CreatedAt  time.Time
}

// AuditEntry is one row of audit_logs; ActorRole and Reason travel in the metadata.
type AuditEntry struct {
	ID            string
	ActorID       *string
	Actor         *Ref
	ActorRole     string
	Action        string
	ResourceType  string
	ResourceID    *string
	Reason        *string
	SourceService string
	Metadata      map[string]any
	SourceEventID *string
	CreatedAt     time.Time
}

// UserCopy is safety-svc's copy of an identity account.
type UserCopy struct {
	ID          string
	Username    string
	DisplayName string
	GlobalRole  string
	Status      string
	LockedUntil *time.Time
	CreatedAt   *time.Time
	OpenReports int
}

// Effective returns the copy as seen now: an expired lock reads as ACTIVE.
func (u UserCopy) Effective(now time.Time) UserCopy {
	if u.Status == UserLocked && u.LockedUntil != nil && !now.Before(*u.LockedUntil) {
		u.Status, u.LockedUntil = UserActive, nil
	}

	return u
}

// ChannelCopy is safety-svc's copy of a channel.
type ChannelCopy struct {
	ID              string
	OwnerID         string
	Owner           Ref
	Handle          string
	DisplayName     string
	AvatarURL       *string
	Status          string
	SuspendedAt     *time.Time
	CurrentStreamID *string
	OpenReports     int
}

// StreamCopy is safety-svc's copy of a stream.
type StreamCopy struct {
	ID         string
	ChannelID  string
	Title      string
	Status     string
	EndReason  *string
	VODID      *string
	VODDeleted bool
	StartedAt  *time.Time
	EndedAt    *time.Time
}

// TakedownAllowed: a stream on air, or ended with a VOD still to delete (BR-F23).
func (s StreamCopy) TakedownAllowed() bool {
	switch s.Status {
	case StreamLive, StreamInterrupt:
		return true
	case StreamEnded:
		return s.VODID != nil && !s.VODDeleted
	}

	return false
}

// Excerpt cuts s to n runes.
func Excerpt(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	return string([]rune(s)[:n])
}
