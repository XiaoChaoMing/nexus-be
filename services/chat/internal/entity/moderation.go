package entity

import (
	"time"
	"unicode/utf8"
)

// Limits of moderation (NXL-F, NXL-00 §12).
const (
	BlockedTermsMax = 100
	ChannelRoleMax  = 50
	ModReasonMax    = 500
	// MentionNotifyMax users are notified per message, in the order they appear.
	MentionNotifyMax = 5
	// MentionExcerptMax runes of the message travel in chat.mention.v1.
	MentionExcerptMax = 100
	// EvidenceMax messages are captured for a report.
	EvidenceMax = 10
)

// ActorRole is what entitles a user to moderate a channel.
type ActorRole string

// Actor roles, strongest first.
const (
	ActorOwner     ActorRole = "OWNER"
	ActorAdmin     ActorRole = "ADMIN"
	ActorModerator ActorRole = "MODERATOR"
)

// Moderation action types (mod_actions.type).
const (
	ModDelete    = "DELETE"
	ModTimeout   = "TIMEOUT"
	ModUntimeout = "UNTIMEOUT"
	ModBan       = "BAN"
	ModUnban     = "UNBAN"
	ModPin       = "PIN"
	ModUnpin     = "UNPIN"
)

// ModAction is one row of mod_actions.
type ModAction struct {
	ID           string
	ChannelID    string
	StreamID     *string
	ActorID      string
	ActorRole    ActorRole
	Type         string
	TargetUserID *string
	MessageID    *string
	Reason       *string
	ExpiresAt    *time.Time
	CreatedAt    time.Time
}

// RestrictionType -.
type RestrictionType string

// Restriction types.
const (
	RestrictBan     RestrictionType = "BAN"
	RestrictTimeout RestrictionType = "TIMEOUT"
)

// Valid -.
func (t RestrictionType) Valid() bool { return t == RestrictBan || t == RestrictTimeout }

// UserRef is a user as lists show it; the identity copy has no avatar.
type UserRef struct {
	ID          string
	Username    string
	DisplayName string
}

// ActiveRestriction is a ban, or a timeout that has not expired, of one user in a channel.
type ActiveRestriction struct {
	ID        string
	User      UserRef
	Type      RestrictionType
	Until     *time.Time
	Actor     UserRef
	Reason    *string
	CreatedAt time.Time
}

// Current is the restriction of the caller shown in the chat: snapshot.
func (r Restriction) Current(now time.Time) *RestrictionState {
	switch {
	case r.Banned:
		return &RestrictionState{Type: RestrictBan}
	case r.TimedOutUntil != nil && r.TimedOutUntil.After(now):
		until := r.TimedOutUntil.UTC()

		return &RestrictionState{Type: RestrictTimeout, Until: &until}
	}

	return nil
}

// RestrictionState is me.restriction of the chat: snapshot and the user card.
type RestrictionState struct {
	Type  RestrictionType `json:"type"`
	Until *time.Time      `json:"until"`
}

// BlockedTerm is one of a channel's blocked terms. Term is the normalized form matched against messages,
// Display what the moderator typed.
type BlockedTerm struct {
	ID        string
	ChannelID string
	Term      string
	Display   string
	CreatedBy UserRef
	CreatedAt time.Time
}

// RoleGrant is a moderator or VIP role in a channel.
type RoleGrant struct {
	ChannelID string
	User      UserRef
	Role      ChannelRole
	GrantedBy UserRef
	CreatedAt time.Time
	// Previous is the role the user had before an assignment changed it.
	Previous *ChannelRole
}

// Valid -.
func (r ChannelRole) Valid() bool { return r == ChannelRoleModerator || r == ChannelRoleVIP }

// ChannelStatus mirrors channel-svc's channels.status.
type ChannelStatus string

// Channel statuses.
const (
	ChannelActive    ChannelStatus = "ACTIVE"
	ChannelSuspended ChannelStatus = "SUSPENDED"
)

// ChannelInfo is chat-svc's copy of a channel's public names.
type ChannelInfo struct {
	ID          string
	OwnerID     string
	Handle      string
	DisplayName string
	AvatarURL   *string
	Status      ChannelStatus
}

// ModChannel is a channel the user moderates (or is VIP of).
type ModChannel struct {
	Channel ChannelInfo
	IsLive  bool
	Role    ChannelRole
}

// UserCard is a user as seen from a channel's chat.
type UserCard struct {
	User      UserRef
	CreatedAt *time.Time
	Badges    []string
	// Role is OWNER, MODERATOR, VIP or empty.
	Role       string
	FollowedAt *time.Time
	// Restriction is only filled for callers who may moderate the channel.
	Restriction *RestrictionState
}

// EvidenceItem is a message captured for a report; deleted messages keep their original text.
type EvidenceItem struct {
	MessageID string
	UserID    string
	Body      string
	Fragments []Fragment
	CreatedAt time.Time
	Deleted   bool
}

// Control is an order every chat-svc instance applies to its own connections (core NATS).
type Control struct {
	UserID string `json:"userId"`
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// CleanReason trims a moderator's reason; empty becomes nil, beyond ModReasonMax runes is ErrValidation.
func CleanReason(r *string) (*string, error) {
	if r == nil {
		return nil, nil //nolint:nilnil // no reason given
	}

	s := collapseSpaces(*r)
	if s == "" {
		return nil, nil //nolint:nilnil // an empty reason is no reason
	}

	if utf8.RuneCountInString(s) > ModReasonMax {
		return nil, Reject(ErrValidation, "reason", DetailLength)
	}

	return &s, nil
}
