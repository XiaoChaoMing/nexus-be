package entity

import "time"

// UserStatus mirrors users.status.
type UserStatus string

// User statuses.
const (
	UserActive  UserStatus = "ACTIVE"
	UserLocked  UserStatus = "LOCKED"
	UserDeleted UserStatus = "DELETED"
)

// Global roles.
const (
	RoleUser  = "USER"
	RoleAdmin = "ADMIN"
)

// Roles returned by /v1/me.
const (
	RoleNameViewer  = "VIEWER"
	RoleNameCreator = "CREATOR"
	RoleNameAdmin   = "ADMIN"
)

// Lock kinds (users.lock_kind): a failed-login lock expires and a password reset lifts it; an admin lock is
// lifted only by an admin or its expiry.
const (
	LockKindFailedLogin = "FAILED_LOGIN"
	LockKindAdmin       = "ADMIN"
)

// Account defaults (users table defaults).
const (
	DefaultLocale   = "vi"
	DefaultTimezone = "Asia/Ho_Chi_Minh"
)

// Preferences is users.preferences (jsonb); unknown keys are kept by the database.
type Preferences struct {
	SidebarCollapsed bool `json:"sidebarCollapsed"`
	MatureVisible    bool `json:"matureVisible"`
}

// User is an account in nxl_identity.users.
type User struct {
	ID              string
	Username        string
	DisplayName     string
	Email           string
	EmailVerifiedAt *time.Time
	// BirthDate is a calendar date (UTC midnight).
	BirthDate         *time.Time
	Locale            string
	Timezone          string
	GlobalRole        string
	Status            UserStatus
	LockedUntil       *time.Time
	LockKind          *string
	LockReason        *string
	FailedLogins      int
	LastFailedLoginAt *time.Time
	Preferences       Preferences
	// ChannelID is set once channel-svc created the user's channel (user_channels).
	ChannelID *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsLocked reports whether the account cannot sign in at the given time.
func (u User) IsLocked(now time.Time) bool {
	if u.Status != UserLocked {
		return false
	}

	return u.LockedUntil == nil || now.Before(*u.LockedUntil)
}

// Effective returns the user as seen now: a lock that has expired reads as ACTIVE.
func (u User) Effective(now time.Time) User {
	if u.Status == UserLocked && u.LockedUntil != nil && !now.Before(*u.LockedUntil) {
		u.Status, u.LockedUntil, u.LockKind, u.LockReason = UserActive, nil, nil, nil
	}

	return u
}

// IsTemporarilyLocked reports a lock from failed sign-ins that has not expired yet.
func (u User) IsTemporarilyLocked(now time.Time) bool {
	return u.Status == UserLocked && u.LockedUntil != nil && now.Before(*u.LockedUntil)
}

// IsPermanentlyLocked reports a lock set by an admin (no end date).
func (u User) IsPermanentlyLocked() bool {
	return u.Status == UserLocked && u.LockedUntil == nil
}

// CanSignIn reports whether the account exists for authentication purposes.
func (u User) CanSignIn() bool {
	return u.Status != UserDeleted
}

// IsEmailVerified -.
func (u User) IsEmailVerified() bool {
	return u.EmailVerifiedAt != nil
}

// IsCreator reports whether the user owns a channel.
func (u User) IsCreator() bool {
	return u.ChannelID != nil
}

// Roles lists the platform roles: always VIEWER, plus CREATOR and ADMIN when they apply.
func (u User) Roles() []string {
	roles := []string{RoleNameViewer}

	if u.IsCreator() {
		roles = append(roles, RoleNameCreator)
	}

	if u.GlobalRole == RoleAdmin {
		roles = append(roles, RoleNameAdmin)
	}

	return roles
}

// DateLayout is the wire format of calendar dates (birth dates).
const DateLayout = "2006-01-02"

const _minBirthYear = 1900

// ParseBirthDate parses a YYYY-MM-DD birth date that is not after today (a calendar day in the
// reference time zone); ErrInvalidBirthDate otherwise. The result is UTC midnight.
func ParseBirthDate(s string, today time.Time) (time.Time, error) {
	d, err := time.Parse(DateLayout, s)
	if err != nil || d.Year() < _minBirthYear {
		return time.Time{}, ErrInvalidBirthDate
	}

	todayDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	if d.After(todayDate) {
		return time.Time{}, ErrInvalidBirthDate
	}

	return d, nil
}
