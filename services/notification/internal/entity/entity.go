// Package entity holds the notification-svc domain: notification types, their defaults (NXL-E BR-E02),
// notifications, settings, e-mails and the copies of users, channels and follows.
package entity

import (
	"encoding/json"
	"time"
)

// Type is a notification type (NXL-E BR-E01).
type Type string

// Notification types.
const (
	TypeCreatorLive        Type = "CREATOR_LIVE"
	TypeStreamStarting     Type = "STREAM_STARTING"
	TypeSchedule           Type = "SCHEDULE"
	TypeMention            Type = "MENTION"
	TypeNewFollower        Type = "NEW_FOLLOWER"
	TypeCreatorApplication Type = "CREATOR_APPLICATION"
	TypeReportResult       Type = "REPORT_RESULT"
	TypeSystem             Type = "SYSTEM"
)

// Types are the settable types in the order of BR-E01 (SUBSCRIPTION comes with GĐ6). The index of a type is
// also its byte in unsubscribe tokens, so the order must never change.
func Types() []Type {
	return []Type{
		TypeCreatorLive, TypeStreamStarting, TypeSchedule, TypeMention, TypeNewFollower, TypeCreatorApplication,
		TypeReportResult, TypeSystem,
	}
}

// Valid -.
func (t Type) Valid() bool {
	_, ok := defaults()[t]

	return ok
}

// Default is a type's default setting and which of its switches the user may change.
type Default struct {
	InApp, Email                bool
	InAppLocked, EmailLocked    bool
	EmailAvailable, EmailForced bool
}

// defaults is the BR-E02 table; EmailForced marks e-mails that skip the daily cap (BR-E07 locked types).
func defaults() map[Type]Default {
	return map[Type]Default{
		TypeCreatorLive:        {InApp: true, Email: true, EmailAvailable: true},
		TypeStreamStarting:     {InApp: true, Email: true, EmailAvailable: true},
		TypeSchedule:           {InApp: true, Email: true, EmailAvailable: true},
		TypeMention:            {InApp: true, EmailAvailable: true},
		TypeNewFollower:        {InApp: true, EmailLocked: true},
		TypeCreatorApplication: {InApp: true, Email: true, InAppLocked: true, EmailLocked: true, EmailAvailable: true, EmailForced: true},
		TypeReportResult:       {InApp: true, EmailAvailable: true},
		TypeSystem:             {InApp: true, Email: true, InAppLocked: true, EmailLocked: true, EmailAvailable: true, EmailForced: true},
	}
}

// DefaultOf returns the defaults of t (zero for an unknown type).
func DefaultOf(t Type) Default { return defaults()[t] }

// Setting is the effective in-app / e-mail choice of one type.
type Setting struct {
	Type  Type
	InApp bool
	Email bool
}

// Effective merges a stored override into the defaults; locked switches always keep their default.
func Effective(t Type, override *Setting) Setting {
	d := DefaultOf(t)
	s := Setting{Type: t, InApp: d.InApp, Email: d.Email}

	if override != nil {
		if !d.InAppLocked {
			s.InApp = override.InApp
		}

		if !d.EmailLocked {
			s.Email = override.Email
		}
	}

	return s
}

// IsDefault reports whether s equals the defaults of its type (such rows are not stored).
func (s Setting) IsDefault() bool {
	d := DefaultOf(s.Type)

	return s.InApp == d.InApp && s.Email == d.Email
}

// Notification is one in-app notification row.
type Notification struct {
	ID         string
	UserID     string
	Type       Type
	Payload    json.RawMessage
	Link       *string
	GroupKey   *string
	GroupCount int
	ReadAt     *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Read -.
func (n Notification) Read() bool { return n.ReadAt != nil }

// Email is an e-mail to queue in email_outbox; Data feeds the template (absolute links included).
type Email struct {
	UserID    string
	To        string
	Template  string
	Locale    string
	Data      map[string]string
	Mandatory bool
}

// OutboxEmail is a queued e-mail picked by the worker.
type OutboxEmail struct {
	ID       string
	To       string
	Template string
	Locale   string
	Data     map[string]string
	Attempts int
}

// Message is a rendered e-mail.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// User statuses of the copy.
const (
	UserActive  = "ACTIVE"
	UserLocked  = "LOCKED"
	UserDeleted = "DELETED"
)

// Channel statuses of the copy.
const (
	ChannelActive    = "ACTIVE"
	ChannelSuspended = "SUSPENDED"
)

// Defaults of a user copy built from events published before locale / timezone existed.
const (
	DefaultLocale   = "vi"
	DefaultTimezone = "Asia/Ho_Chi_Minh"
)

// UserCopy is the copy of an identity account; nil fields are left unchanged by an update.
type UserCopy struct {
	ID            string
	Username      *string
	DisplayName   *string
	Email         *string
	EmailVerified *bool
	Locale        *string
	Timezone      *string
	Status        *string
	LockedUntil   *time.Time
}

// ChannelCopy is the copy of a channel-svc channel.
type ChannelCopy struct {
	ID          string
	OwnerID     string
	Handle      string
	DisplayName string
	AvatarURL   *string
	Status      string
}

// ChannelPatch updates the copy of a channel; nil fields are left unchanged (SetAvatar applies AvatarURL).
type ChannelPatch struct {
	ID          string
	OwnerID     *string
	Handle      *string
	DisplayName *string
	AvatarURL   *string
	SetAvatar   bool
	Status      *string
}

// Follow is the copy of a follow.
type Follow struct {
	UserID      string
	ChannelID   string
	NotifyLevel string
	CreatedAt   time.Time
}

// Recipient is who a notification or e-mail goes to, from the user copy.
type Recipient struct {
	UserID        string
	Username      string
	DisplayName   string
	Email         string
	EmailVerified bool
	Locale        string
	Timezone      string
	// Active is false for a locked (lock not expired) or deleted account.
	Active bool
}

// Name is what e-mails call the recipient.
func (r Recipient) Name() string {
	if r.DisplayName != "" {
		return r.DisplayName
	}

	return r.Username
}
