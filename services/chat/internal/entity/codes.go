package entity

import "errors"

// Error codes of NXL-00 §5, shared by the WebSocket error frame, the REST envelope and snapshots.
const (
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeRateLimited      = "RATE_LIMITED"
	CodeInternal         = "INTERNAL_ERROR"
	CodeEmailNotVerified = "EMAIL_NOT_VERIFIED"
	CodeChatClosed       = "CHAT_CLOSED"
	CodeChatReadOnly     = "CHAT_READ_ONLY"
	CodeChatDisabled     = "CHAT_DISABLED"
	CodeChatDuplicate    = "CHAT_DUPLICATE"
	CodeChatSlowMode     = "CHAT_SLOW_MODE"
	CodeFollowersOnly    = "CHAT_FOLLOWERS_ONLY"
	CodeSubsOnly         = "CHAT_SUBS_ONLY"
	CodeTimedOut         = "CHAT_TIMED_OUT"
	CodeBanned           = "CHAT_BANNED"
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
		{ErrInvalidTopic, CodeValidationFailed},
		{ErrUnauthenticated, CodeUnauthorized},
		{ErrInvalidToken, CodeUnauthorized},
		{ErrForbidden, CodeForbidden},
		{ErrNotFound, CodeNotFound},
		{ErrRateLimited, CodeRateLimited},
		{ErrTooManySubscriptions, CodeRateLimited},
		{ErrTooManyConnections, CodeRateLimited},
		{ErrEmailNotVerified, CodeEmailNotVerified},
		{ErrRoomClosed, CodeChatClosed},
		{ErrRoomReadOnly, CodeChatReadOnly},
		{ErrChatDisabled, CodeChatDisabled},
		{ErrDuplicate, CodeChatDuplicate},
		{ErrSlowMode, CodeChatSlowMode},
		{ErrFollowersOnly, CodeFollowersOnly},
		{ErrSubsOnly, CodeSubsOnly},
		{ErrTimedOut, CodeTimedOut},
		{ErrBanned, CodeBanned},
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
