package entity

import (
	"errors"
	"maps"
)

var (
	// ErrNotFound: no such notification of the caller (or a copy the event needs is missing).
	ErrNotFound = errors.New("not found")
	// ErrValidation: a field rule failed; RejectError.Details names the fields.
	ErrValidation = errors.New("validation failed")
	// ErrLockedType: a locked switch was turned off or e-mail turned on for a type without e-mail.
	ErrLockedType = errors.New("type cannot be changed")
	// ErrTokenInvalid: the unsubscribe token is malformed, forged or names a type without e-mail.
	ErrTokenInvalid = errors.New("invalid token")
	// ErrBadCursor: the list cursor cannot be decoded.
	ErrBadCursor = errors.New("bad cursor")
	// ErrUnknownPurpose: auth.email_requested.v1 with a purpose that has no template (skipped).
	ErrUnknownPurpose = errors.New("unknown e-mail purpose")
	// ErrMailDisabled: no SMTP server is configured; e-mails are only logged.
	ErrMailDisabled = errors.New("mail disabled")
)

// Field detail codes (NXL-00 §4.1).
const (
	DetailRequired = "REQUIRED"
	DetailEnum     = "ENUM"
	DetailInvalid  = "INVALID"
	DetailFormat   = "FORMAT"
	DetailLength   = "LENGTH"
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
