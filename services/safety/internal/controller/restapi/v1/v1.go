// Package v1 is the public REST API of safety-svc.
package v1

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/safety/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

// Error codes shared with the web and admin clients.
const (
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeInvalidState     = "INVALID_STATE"
	CodeRateLimited      = "RATE_LIMITED"
	CodeInternal         = "INTERNAL_ERROR"

	_detailFormat = "FORMAT"
)

// UseCases are the business contracts the routes call.
type UseCases struct {
	Reports   usecase.Reports
	Queue     usecase.AdminReports
	Actions   usecase.AdminActions
	Directory usecase.Directory
	Audit     usecase.Audit
}

type routes struct {
	uc UseCases
	l  logger.Interface
}

// NewRoutes registers /v1/reports and /v1/admin/*; auth verifies the token, admin requires role ADMIN.
func NewRoutes(group fiber.Router, uc UseCases, auth, admin fiber.Handler, l logger.Interface) {
	r := &routes{uc: uc, l: l}

	group.Post("/reports", auth, r.fileReport)

	a := group.Group("/admin", auth, admin)
	{
		a.Get("/reports", r.listReports)
		a.Get("/reports/open-count", r.openCount)
		a.Get("/reports/:id", r.getReport)
		a.Post("/reports/:id/claim", r.claim)
		a.Post("/reports/:id/resolve", r.resolve)
		a.Get("/users", r.users)
		a.Post("/users/:userId/lock", r.lock)
		a.Post("/users/:userId/unlock", r.unlock)
		a.Get("/channels", r.channels)
		a.Get("/channels/:channelId", r.channel)
		a.Post("/channels/:channelId/suspend", r.suspend)
		a.Post("/channels/:channelId/restore", r.restore)
		a.Post("/streams/:streamId/takedown", r.takedown)
		a.Get("/audit-log", r.auditLog)
	}
}

func errorJSON(ctx *fiber.Ctx, status int, code, message string, details map[string]string) error {
	return ctx.Status(status).JSON(ErrorResponse{Error: ErrorBody{Code: code, Message: message, Details: details}})
}

func validationFailed(ctx *fiber.Ctx, details map[string]string) error {
	return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "validation failed", details)
}

// fail maps a use-case error to the NXL-00 §4 error body.
func (r *routes) fail(ctx *fiber.Ctx, op string, err error) error {
	var rl *entity.RateLimitError

	switch {
	case errors.Is(err, entity.ErrValidation):
		return validationFailed(ctx, entity.DetailsOf(err))
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, entity.ErrForbidden):
		return errorJSON(ctx, http.StatusForbidden, CodeForbidden, "forbidden", nil)
	case errors.Is(err, entity.ErrInvalidState):
		return errorJSON(ctx, http.StatusConflict, CodeInvalidState, "invalid state", nil)
	case errors.As(err, &rl):
		sec := strconv.Itoa(int(math.Ceil(rl.RetryAfter.Seconds())))
		ctx.Set(fiber.HeaderRetryAfter, sec)

		return errorJSON(ctx, http.StatusTooManyRequests, CodeRateLimited, "too many reports",
			map[string]string{"retryAfterSec": sec})
	}

	r.l.Error("restapi - v1 - %s: %v", op, err)

	return errorJSON(ctx, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
}

func caller(ctx *fiber.Ctx) (string, error) {
	id, ok := middleware.UserID(ctx)
	if !ok {
		return "", errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	return id, nil
}

// pageQuery reads cursor and limit (1–100).
func pageQuery(ctx *fiber.Ctx) (cur string, limit int, ok bool) {
	if s := ctx.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			return "", 0, false
		}

		limit = n
	}

	return ctx.Query("cursor"), limit, true
}

// csv splits a comma-separated query value.
func csv(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

func optional(ctx *fiber.Ctx, key string) *string {
	if v := ctx.Query(key); v != "" {
		return &v
	}

	return nil
}

func optionalTime(ctx *fiber.Ctx, key string) (*time.Time, bool) {
	v := ctx.Query(key)
	if v == "" {
		return nil, true
	}

	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, false
	}

	return &t, true
}
