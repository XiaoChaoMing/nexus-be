package v1

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
)

// Error codes shared with the web clients (NXL-00 §5).
const (
	CodeValidationFailed   = "VALIDATION_FAILED"
	CodeUsernameTaken      = "USERNAME_TAKEN"
	CodeEmailTaken         = "EMAIL_TAKEN"
	CodeInvalidCredentials = "INVALID_CREDENTIALS" //nolint:gosec // an error code, not a credential
	CodeAccountLocked      = "ACCOUNT_LOCKED"
	CodeSessionExpired     = "SESSION_EXPIRED"
	CodeUnauthorized       = "UNAUTHORIZED"
	CodeForbidden          = "FORBIDDEN"
	CodeNotFound           = "NOT_FOUND"
	CodeConflict           = "CONFLICT"
	CodeRateLimited        = "RATE_LIMITED"
	CodeTokenInvalid       = "TOKEN_INVALID"
	CodeWrongPassword      = "WRONG_PASSWORD"
	CodeEmailNotVerified   = "EMAIL_NOT_VERIFIED"
	CodeApplicationPending = "APPLICATION_PENDING"
	CodeAlreadyCreator     = "ALREADY_CREATOR"
	CodeUnderage           = "UNDERAGE"
	CodeInvalidState       = "INVALID_STATE"
	CodeInternal           = "INTERNAL_ERROR"

	// Field detail values.
	_detailTaken      = "TAKEN"
	_detailInvalid    = "INVALID"
	_detailAlreadySet = "ALREADY_SET"
)

func errorJSON(ctx *fiber.Ctx, status int, code, message string, details map[string]string) error {
	return ctx.Status(status).JSON(response.Error{Error: response.ErrorBody{Code: code, Message: message, Details: details}})
}

func validationFailed(ctx *fiber.Ctx, details map[string]string) error {
	return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", details)
}

// sentinelMapping is the transport form of one domain error.
type sentinelMapping struct {
	err     error
	status  int
	code    string
	message string
	details map[string]string
}

func sentinelMappings() []sentinelMapping {
	return []sentinelMapping{
		{entity.ErrUsernameTaken, http.StatusConflict, CodeUsernameTaken, "username already taken", map[string]string{"username": _detailTaken}},
		{entity.ErrEmailTaken, http.StatusConflict, CodeEmailTaken, "email already registered", map[string]string{"email": _detailTaken}},
		{entity.ErrInvalidCredentials, http.StatusUnauthorized, CodeInvalidCredentials, "invalid login or password", nil},
		{entity.ErrAccountLocked, http.StatusForbidden, CodeAccountLocked, "account is locked", nil},
		{entity.ErrUserNotFound, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil},
		{entity.ErrTokenInvalid, http.StatusBadRequest, CodeTokenInvalid, "link is invalid or expired", nil},
		{entity.ErrAlreadyVerified, http.StatusConflict, CodeConflict, "email already verified", nil},
		{entity.ErrWrongPassword, http.StatusBadRequest, CodeWrongPassword, "current password is wrong", nil},
		{entity.ErrBirthDateAlreadySet, http.StatusBadRequest, CodeValidationFailed, "birth date already set", map[string]string{"birthDate": _detailAlreadySet}},
		{entity.ErrInvalidBirthDate, http.StatusBadRequest, CodeValidationFailed, "invalid birth date", map[string]string{"birthDate": _detailInvalid}},
		{entity.ErrInvalidCursor, http.StatusBadRequest, CodeValidationFailed, "invalid cursor", map[string]string{"cursor": _detailInvalid}},
		{entity.ErrEmailNotVerified, http.StatusForbidden, CodeEmailNotVerified, "email not verified", nil},
		{entity.ErrApplicationPending, http.StatusConflict, CodeApplicationPending, "an application is already pending", nil},
		{entity.ErrAlreadyCreator, http.StatusConflict, CodeAlreadyCreator, "already a creator", nil},
		{entity.ErrUnderage, http.StatusUnprocessableEntity, CodeUnderage, "below the minimum creator age", nil},
		{entity.ErrInvalidState, http.StatusConflict, CodeInvalidState, "application is no longer pending", nil},
		{entity.ErrApplicationNotFound, http.StatusNotFound, CodeNotFound, "application not found", nil},
		{entity.ErrSelfReview, http.StatusForbidden, CodeForbidden, "cannot review your own application", nil},
		{entity.ErrNotFound, http.StatusNotFound, CodeNotFound, "user not found", nil},
		{entity.ErrSelfBlock, http.StatusBadRequest, CodeValidationFailed, "cannot block yourself", map[string]string{"userId": _detailInvalid}},
		{entity.ErrBlockLimit, http.StatusConflict, CodeConflict, "block limit reached", map[string]string{"limit": strconv.Itoa(entity.BlockMax)}},
	}
}

// mapError turns a use-case error into a transport response; unexpected errors become a generic 500.
func (r *V1) mapError(ctx *fiber.Ctx, op string, err error) error {
	var (
		locked *entity.LockedError
		retry  *entity.RetryError
	)

	switch {
	case errors.As(err, &locked):
		return errorJSON(ctx, http.StatusForbidden, CodeAccountLocked, "account is temporarily locked",
			map[string]string{"lockedUntil": locked.Until.UTC().Format(time.RFC3339)})
	case errors.As(err, &retry):
		secs := strconv.Itoa(int(math.Ceil(retry.RetryAfter.Seconds())))
		ctx.Set(fiber.HeaderRetryAfter, secs)

		return errorJSON(ctx, http.StatusTooManyRequests, CodeRateLimited, "too many requests", map[string]string{"retryAfterSec": secs})
	case errors.Is(err, entity.ErrSessionExpired):
		r.clearRefreshCookie(ctx)

		return errorJSON(ctx, http.StatusUnauthorized, CodeSessionExpired, "session expired", nil)
	}

	for _, m := range sentinelMappings() {
		if errors.Is(err, m.err) {
			return errorJSON(ctx, m.status, m.code, m.message, m.details)
		}
	}

	r.l.Error("restapi - v1 - %s: %v", op, err)

	return errorJSON(ctx, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
}
