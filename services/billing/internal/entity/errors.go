package entity

import (
	"errors"
	"maps"
	"time"
)

var (
	// ErrNotFound -: missing, someone else's, or a malformed id.
	ErrNotFound = errors.New("not found")
	// ErrNotCreator: the caller owns no channel.
	ErrNotCreator = errors.New("caller has no channel")
	// ErrForbidden -.
	ErrForbidden = errors.New("forbidden")
	// ErrChannelSuspended -.
	ErrChannelSuspended = errors.New("channel suspended")
	// ErrSubscribeSelf -.
	ErrSubscribeSelf = errors.New("cannot subscribe to own channel")
	// ErrValidation: a field rule failed; RejectError.Details names the fields.
	ErrValidation = errors.New("validation failed")
	// ErrInvalidState: StateError carries details.reason.
	ErrInvalidState = errors.New("invalid state")
	// ErrPendingLimit: CHECKOUT_MAX_PENDING pending orders.
	ErrPendingLimit = errors.New("too many pending orders")
	// ErrRateLimited: RateLimitError carries the wait.
	ErrRateLimited = errors.New("rate limited")
	// ErrIdemReused: same Idempotency-Key, different body.
	ErrIdemReused = errors.New("idempotency key reused")
	// ErrIdemInProgress: the first request with the key is still running.
	ErrIdemInProgress = errors.New("idempotency key in progress")
	// ErrBadCursor -.
	ErrBadCursor = errors.New("bad cursor")

	// ErrUnauthorized: the notification secret does not match (webhook 401).
	ErrUnauthorized = errors.New("notification secret mismatch")
	// ErrMalformed: the notification cannot be parsed (webhook 400).
	ErrMalformed = errors.New("malformed notification")
	// ErrProviderUnavailable: timeout, 5xx, 429 or rejected credentials.
	ErrProviderUnavailable = errors.New("payment provider unavailable")
	// ErrProviderNotFound: the provider does not know the invoice (never submitted).
	ErrProviderNotFound = errors.New("order unknown to provider")
	// ErrDecrypt: the stored bank account cannot be decrypted.
	ErrDecrypt = errors.New("cannot decrypt bank account")
)

// Field detail codes (NXL-00 §4.1).
const (
	DetailRequired = "REQUIRED"
	DetailLength   = "LENGTH"
	DetailEnum     = "ENUM"
	DetailInvalid  = "INVALID"
	DetailFormat   = "FORMAT"
	DetailMin      = "MIN"
	DetailMax      = "MAX"
)

// Reasons of INVALID_STATE (details.reason).
const (
	ReasonTierInactive        = "TIER_INACTIVE"
	ReasonTierChange          = "TIER_CHANGE"
	ReasonMaxPrepaid          = "MAX_PREPAID"
	ReasonOrderPending        = "ORDER_PENDING"
	ReasonInsufficientBalance = "INSUFFICIENT_BALANCE"
	ReasonPayoutPending       = "PAYOUT_PENDING"
)

// RejectError adds machine-readable details to a sentinel.
type RejectError struct {
	Err     error
	Details map[string]string
}

// Reject wraps err with details given as key, value pairs.
func Reject(err error, kv ...string) *RejectError {
	d := make(map[string]string, len(kv)/2) //nolint:mnd // pairs
	for i := 0; i+1 < len(kv); i += 2 {
		d[kv[i]] = kv[i+1]
	}

	return &RejectError{Err: err, Details: d}
}

func (e *RejectError) Error() string { return e.Err.Error() }

// Unwrap lets errors.Is match the sentinel.
func (e *RejectError) Unwrap() error { return e.Err }

// Invalid is ErrValidation with one field and detail code.
func Invalid(field, code string) error { return Reject(ErrValidation, field, code) }

// StateError is ErrInvalidState with details.reason and extra keys.
func StateError(reason string, kv ...string) error {
	return Reject(ErrInvalidState, append([]string{"reason", reason}, kv...)...)
}

// DetailsOf returns a copy of the details carried by err, or nil.
func DetailsOf(err error) map[string]string {
	var re *RejectError
	if errors.As(err, &re) && len(re.Details) > 0 {
		return maps.Clone(re.Details)
	}

	return nil
}

// RateLimitError is ErrRateLimited with the time to wait.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return "rate limited, retry after " + e.RetryAfter.String() }

// Is -.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// ProviderError is ErrProviderUnavailable; Server marks a 5xx answer (sync ERROR → PROVIDER_ERROR).
type ProviderError struct {
	Server bool
	Status int
}

func (e *ProviderError) Error() string { return "payment provider unavailable" }

// Is -.
func (e *ProviderError) Is(target error) bool { return target == ErrProviderUnavailable }
