package entity

import (
	"errors"
	"maps"
	"time"
)

var (
	// ErrNotFound -.
	ErrNotFound = errors.New("not found")
	// ErrForbidden: the target is an admin.
	ErrForbidden = errors.New("forbidden")
	// ErrValidation: a field rule failed; RejectError.Details names the fields.
	ErrValidation = errors.New("validation failed")
	// ErrInvalidState: the report is closed, the stream cannot be taken down.
	ErrInvalidState = errors.New("invalid state")
	// ErrRateLimited: REPORT_RATE_LIMIT reached (RateLimitError.RetryAfter).
	ErrRateLimited = errors.New("rate limited")
)

// Field detail codes (NXL-00 §4.1).
const (
	DetailRequired = "REQUIRED"
	DetailLength   = "LENGTH"
	DetailEnum     = "ENUM"
	DetailInvalid  = "INVALID"
	DetailFormat   = "FORMAT"
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

// DetailsOf returns a copy of the details carried by err, or nil.
func DetailsOf(err error) map[string]string {
	var re *RejectError
	if errors.As(err, &re) && len(re.Details) > 0 {
		return maps.Clone(re.Details)
	}

	return nil
}

// Invalid is ErrValidation with field = INVALID.
func Invalid(field string) error { return Reject(ErrValidation, field, DetailInvalid) }

// RateLimitError is ErrRateLimited with the time to wait.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return "rate limited, retry after " + e.RetryAfter.String() }

// Is -.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }
