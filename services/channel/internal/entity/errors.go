package entity

import (
	"errors"
	"maps"
)

var (
	// ErrNotFound -.
	ErrNotFound = errors.New("not found")
	// ErrSuspended -.
	ErrSuspended = errors.New("channel suspended")
	// ErrNoFreeHandle means every suffixed candidate is taken (practically unreachable).
	ErrNoFreeHandle = errors.New("no free handle")
	// ErrValidation: input failed a field rule; RejectError.Details names the fields.
	ErrValidation = errors.New("validation failed")
	// ErrCannotFollowSelf -.
	ErrCannotFollowSelf = errors.New("cannot follow own channel")
	// ErrNotFollowing: the bell of a channel the user does not follow.
	ErrNotFollowing = errors.New("not following")
	// ErrNotCreator: the caller owns no channel, so the Studio is closed to them.
	ErrNotCreator = errors.New("caller has no channel")
	// ErrHandleTaken -.
	ErrHandleTaken = errors.New("handle taken")
	// ErrHandleTooSoon: the handle changed less than HANDLE_CHANGE_INTERVAL ago (details.nextChangeAt).
	ErrHandleTooSoon = errors.New("handle changed recently")
	// ErrImageTooLarge -.
	ErrImageTooLarge = errors.New("image too large")
	// ErrImageFormat: not a JPEG, PNG or WebP that decodes.
	ErrImageFormat = errors.New("unsupported image")
	// ErrImageTooSmall -.
	ErrImageTooSmall = errors.New("image too small")
	// ErrScheduleLimit: SCHEDULE_MAX_ACTIVE reached (details.limit).
	ErrScheduleLimit = errors.New("too many active schedules")
	// ErrInvalidState: the transition is not allowed in the current state.
	ErrInvalidState = errors.New("invalid state")
)

// Error codes of NXL-00 §5 returned by channel-svc.
const (
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeNotFound         = "NOT_FOUND"
	CodeChannelSuspended = "CHANNEL_SUSPENDED"
	CodeCannotFollowSelf = "CANNOT_FOLLOW_SELF"
	CodeNotCreator       = "NOT_CREATOR"
	CodeHandleTaken      = "HANDLE_TAKEN"
	CodeConflict         = "CONFLICT"
	CodePayloadTooLarge  = "PAYLOAD_TOO_LARGE"
	CodeInvalidState     = "INVALID_STATE"
	CodeInternal         = "INTERNAL_ERROR"
)

// Detail values of VALIDATION_FAILED (NXL-00 §5.3).
const (
	DetailRequired = "REQUIRED"
	DetailFormat   = "FORMAT"
	DetailLength   = "LENGTH"
	DetailMin      = "MIN"
	DetailMax      = "MAX"
	DetailEnum     = "ENUM"
	DetailInvalid  = "INVALID"
	DetailTaken    = "TAKEN"
)

func codeTable() []struct {
	err  error
	code string
} {
	return []struct {
		err  error
		code string
	}{
		{ErrValidation, CodeValidationFailed},
		{ErrImageFormat, CodeValidationFailed},
		{ErrImageTooSmall, CodeValidationFailed},
		{ErrNotFound, CodeNotFound},
		{ErrNotFollowing, CodeNotFound},
		{ErrSuspended, CodeChannelSuspended},
		{ErrCannotFollowSelf, CodeCannotFollowSelf},
		{ErrNotCreator, CodeNotCreator},
		{ErrHandleTaken, CodeHandleTaken},
		{ErrHandleTooSoon, CodeConflict},
		{ErrScheduleLimit, CodeConflict},
		{ErrImageTooLarge, CodePayloadTooLarge},
		{ErrInvalidState, CodeInvalidState},
	}
}

// CodeOf maps a domain error to its NXL-00 code; anything else is INTERNAL_ERROR.
func CodeOf(err error) string {
	for _, m := range codeTable() {
		if errors.Is(err, m.err) {
			return m.code
		}
	}

	return CodeInternal
}

// RejectError adds machine-readable details (NXL-00 §4 `details`) to a sentinel.
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

// Invalid is a validation failure with the given field details.
func Invalid(details map[string]string) *RejectError {
	return &RejectError{Err: ErrValidation, Details: details}
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
