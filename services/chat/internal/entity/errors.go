package entity

import (
	"errors"
	"maps"
)

var (
	// ErrUnauthenticated: a guest tried to write, or a token is missing.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrInvalidToken: the access token is malformed, expired or signed by an unknown key.
	ErrInvalidToken = errors.New("invalid token")
	// ErrForbidden -.
	ErrForbidden = errors.New("forbidden")
	// ErrNotFound -.
	ErrNotFound = errors.New("not found")
	// ErrValidation: input failed a field rule; RejectError.Details names the fields.
	ErrValidation = errors.New("validation failed")
	// ErrRoomClosed: the room is not open (stream not started) or closed for good.
	ErrRoomClosed = errors.New("chat room closed")
	// ErrRoomReadOnly: the stream ended.
	ErrRoomReadOnly = errors.New("chat room is read only")
	// ErrChatDisabled: the kill switch is on.
	ErrChatDisabled = errors.New("chat disabled")
	// ErrDuplicate: same body from the same user inside the duplicate window.
	ErrDuplicate = errors.New("duplicate message")
	// ErrRateLimited -.
	ErrRateLimited = errors.New("rate limited")
	// ErrSlowMode -.
	ErrSlowMode = errors.New("slow mode")
	// ErrFollowersOnly -.
	ErrFollowersOnly = errors.New("followers only")
	// ErrSubsOnly -.
	ErrSubsOnly = errors.New("subscribers only")
	// ErrTimedOut -.
	ErrTimedOut = errors.New("timed out")
	// ErrBanned -.
	ErrBanned = errors.New("banned")
	// ErrEmailNotVerified: only when CHAT_REQUIRE_VERIFIED_EMAIL is on.
	ErrEmailNotVerified = errors.New("email not verified")
	// ErrInvalidTopic: the topic is not chat:{uuid}, stream:{uuid}, user:{uuid} or sidebar:{uuid}.
	ErrInvalidTopic = errors.New("invalid topic")
	// ErrTooManySubscriptions: WS_MAX_SUBSCRIPTIONS reached on one connection.
	ErrTooManySubscriptions = errors.New("too many subscriptions")
	// ErrTooManyConnections: WS_MAX_CONN_PER_USER reached.
	ErrTooManyConnections = errors.New("too many connections")
)

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
