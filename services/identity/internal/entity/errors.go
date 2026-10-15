package entity

import (
	"errors"
	"time"
)

var (
	// ErrUsernameTaken -.
	ErrUsernameTaken = errors.New("username taken")
	// ErrEmailTaken -.
	ErrEmailTaken = errors.New("email taken")
	// ErrInvalidCredentials covers unknown login and wrong password alike, so accounts cannot be enumerated.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrAccountLocked -.
	ErrAccountLocked = errors.New("account locked")
	// ErrSessionExpired covers missing, expired, revoked and reused refresh tokens.
	ErrSessionExpired = errors.New("session expired")
	// ErrUserNotFound -.
	ErrUserNotFound = errors.New("user not found")
	// ErrSessionNotFound -.
	ErrSessionNotFound = errors.New("session not found")
	// ErrSessionAlreadyRevoked is returned when a rotation loses the race to another one.
	ErrSessionAlreadyRevoked = errors.New("session already revoked")
	// ErrTokenInvalid covers unknown, expired, superseded and already used e-mail tokens.
	ErrTokenInvalid = errors.New("email token invalid")
	// ErrAlreadyVerified -.
	ErrAlreadyVerified = errors.New("email already verified")
	// ErrRateLimited -.
	ErrRateLimited = errors.New("rate limited")
	// ErrWrongPassword is the current password check of a password change.
	ErrWrongPassword = errors.New("wrong password")
	// ErrBirthDateAlreadySet: the birth date can only be set while it is empty.
	ErrBirthDateAlreadySet = errors.New("birth date already set")
	// ErrInvalidBirthDate covers dates in the future or implausibly old.
	ErrInvalidBirthDate = errors.New("invalid birth date")
	// ErrEmailNotVerified -.
	ErrEmailNotVerified = errors.New("email not verified")
	// ErrApplicationPending -.
	ErrApplicationPending = errors.New("creator application pending")
	// ErrAlreadyCreator -.
	ErrAlreadyCreator = errors.New("already a creator")
	// ErrUnderage -.
	ErrUnderage = errors.New("underage")
	// ErrInvalidState is an application state transition that is not allowed.
	ErrInvalidState = errors.New("invalid state")
	// ErrApplicationNotFound -.
	ErrApplicationNotFound = errors.New("creator application not found")
	// ErrSelfReview: an admin cannot review their own application.
	ErrSelfReview = errors.New("cannot review own application")
	// ErrInvalidCursor -.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrNotFound: the user a request targets (block, admin lookup) does not exist or was deleted.
	ErrNotFound = errors.New("not found")
	// ErrSelfBlock: a user cannot block themselves.
	ErrSelfBlock = errors.New("cannot block yourself")
	// ErrBlockLimit: the user already blocks BlockMax accounts.
	ErrBlockLimit = errors.New("block limit reached")
	// ErrChannelTaken: the channel id is already recorded for another user.
	ErrChannelTaken = errors.New("channel already recorded for another user")
)

// LockedError is a temporary lock after too many failed sign-ins; errors.Is matches ErrAccountLocked.
type LockedError struct {
	Until time.Time
}

func (e *LockedError) Error() string {
	return "account locked until " + e.Until.UTC().Format(time.RFC3339)
}

// Is -.
func (e *LockedError) Is(target error) bool {
	return target == ErrAccountLocked
}

// RetryError rejects a request that may be retried after RetryAfter; errors.Is matches ErrRateLimited.
type RetryError struct {
	RetryAfter time.Duration
}

func (e *RetryError) Error() string {
	return "rate limited, retry after " + e.RetryAfter.String()
}

// Is -.
func (e *RetryError) Is(target error) bool {
	return target == ErrRateLimited
}
