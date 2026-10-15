// Package v1 is the public REST API of notification-svc.
package v1

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/notification/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

// Error codes shared with the web client (NXL-00 §5).
const (
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeNotFound         = "NOT_FOUND"
	CodeTokenInvalid     = "TOKEN_INVALID"
	CodeInternal         = "INTERNAL_ERROR"
)

// UseCases are the business contracts the routes call.
type UseCases struct {
	Center   usecase.Center
	Settings usecase.Settings
}

type routes struct {
	uc UseCases
	l  logger.Interface
}

// NewRoutes registers /v1/notifications* and /v1/me/notification-settings; auth verifies the token.
func NewRoutes(group fiber.Router, uc UseCases, auth fiber.Handler, l logger.Interface) {
	r := &routes{uc: uc, l: l}

	// No token: the link comes from an e-mail and may be opened signed out.
	group.Post("/notifications/unsubscribe", r.unsubscribe)

	n := group.Group("/notifications", auth)
	{
		n.Get("", r.list)
		n.Get("/unread-count", r.unreadCount)
		n.Post("/read-all", r.readAll)
		n.Post("/:id/read", r.read)
		n.Delete("/:id", r.remove)
	}

	group.Get("/me/notification-settings", auth, r.getSettings)
	group.Put("/me/notification-settings", auth, r.putSettings)
}

func errorJSON(ctx *fiber.Ctx, status int, code, message string, details map[string]string) error {
	return ctx.Status(status).JSON(ErrorResponse{Error: ErrorBody{Code: code, Message: message, Details: details}})
}

func validationFailed(ctx *fiber.Ctx, details map[string]string) error {
	return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "validation failed", details)
}

// fail maps a use-case error to the NXL-00 §4 error body.
func (r *routes) fail(ctx *fiber.Ctx, op string, err error) error {
	switch {
	case errors.Is(err, entity.ErrValidation), errors.Is(err, entity.ErrLockedType):
		return validationFailed(ctx, entity.DetailsOf(err))
	case errors.Is(err, entity.ErrBadCursor):
		return validationFailed(ctx, map[string]string{"cursor": entity.DetailInvalid})
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, entity.ErrTokenInvalid):
		return errorJSON(ctx, http.StatusBadRequest, CodeTokenInvalid, "invalid token", nil)
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
