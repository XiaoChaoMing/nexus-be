// Package v1 is the public REST API of channel-svc.
package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

// UseCases groups what the routes call.
type UseCases struct {
	Directory usecase.Directory
	Follows   usecase.Follows
	Studio    usecase.Studio
	Catalog   usecase.Catalog
	Search    usecase.Search
	Schedules usecase.Schedules
	Reminders usecase.Reminders
}

// Auth holds the two authentication middlewares: Required rejects guests, Optional identifies callers when a
// token is sent.
type Auth struct {
	Required fiber.Handler
	Optional fiber.Handler
}

// Options -.
type Options struct {
	AvatarMaxBytes int
	BannerMaxBytes int
	// ScheduleWindow is the default length of GET /channels/{id}/schedule; ScheduleHorizon its farthest end.
	ScheduleWindow  time.Duration
	ScheduleHorizon time.Duration
}

// _fieldBody names the whole body in VALIDATION_FAILED details.
const _fieldBody = "body"

type routes struct {
	uc UseCases
	o  Options
	l  logger.Interface
}

// NewRoutes registers every channel-svc route under group (/v1). Static routes come before parameterized
// ones of the same prefix.
func NewRoutes(group fiber.Router, uc UseCases, auth Auth, o Options, l logger.Interface) {
	r := &routes{uc: uc, o: o, l: l}

	group.Get("/channels/:handle", auth.Optional, r.profile)
	group.Post("/channels/:id/follow", auth.Required, r.follow)
	group.Put("/channels/:id/follow", auth.Required, r.setNotifyLevel)
	group.Delete("/channels/:id/follow", auth.Required, r.unfollow)
	group.Get("/channels/:id/schedule", auth.Optional, r.channelSchedule)

	group.Get("/me/following/sidebar", auth.Required, r.sidebar)
	group.Get("/me/following", auth.Required, r.following)
	group.Get("/me/search-history", auth.Required, r.history)
	group.Post("/me/search-history", auth.Required, r.remember)
	group.Delete("/me/search-history", auth.Required, r.forgetAll)
	group.Delete("/me/search-history/:id", auth.Required, r.forget)

	group.Get("/categories", r.categories)
	group.Get("/categories/:slug", r.category)
	group.Get("/search/suggest", r.suggest)
	group.Get("/search", r.search)

	group.Post("/schedules/:id/reminders", auth.Required, r.remind)
	group.Delete("/schedules/:id/reminders", auth.Required, r.unremind)

	st := group.Group("/studio", auth.Required, r.refuseSuspendedWrites)
	{
		st.Get("/channel/stats", r.stats)
		st.Get("/channel", r.studioChannel)
		st.Patch("/channel", r.updateChannel)
		st.Put("/channel/avatar", r.uploadAvatar)
		st.Put("/channel/banner", r.uploadBanner)
		st.Get("/schedules", r.schedules)
		st.Post("/schedules", r.createSchedule)
		st.Put("/schedules/:id", r.updateSchedule)
		st.Delete("/schedules/:id", r.cancelSchedule)
		st.Put("/schedules/:id/occurrences/:occurrenceAt", r.moveOccurrence)
		st.Delete("/schedules/:id/occurrences/:occurrenceAt", r.cancelOccurrence)
	}
}

func errorJSON(ctx *fiber.Ctx, status int, code, message string, details map[string]string) error {
	return ctx.Status(status).JSON(response.Error{Error: response.ErrorBody{Code: code, Message: message, Details: details}})
}

func invalid(ctx *fiber.Ctx, details map[string]string) error {
	return errorJSON(ctx, http.StatusBadRequest, entity.CodeValidationFailed, "invalid request", details)
}

func notFound(ctx *fiber.Ctx) error {
	return errorJSON(ctx, http.StatusNotFound, entity.CodeNotFound, "not found", nil)
}

// statusOf is the HTTP status of each code a route can return (NXL-00 §5).
func statusOf(code string) int {
	switch code {
	case entity.CodeValidationFailed:
		return http.StatusBadRequest
	case entity.CodeUnauthorized:
		return http.StatusUnauthorized
	case entity.CodeNotFound:
		return http.StatusNotFound
	case entity.CodeChannelSuspended, entity.CodeNotCreator:
		return http.StatusForbidden
	case entity.CodeCannotFollowSelf:
		return http.StatusUnprocessableEntity
	case entity.CodeHandleTaken, entity.CodeConflict, entity.CodeInvalidState:
		return http.StatusConflict
	case entity.CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}

// fail maps a use-case error; unexpected errors are logged and answered with a generic message.
func (r *routes) fail(ctx *fiber.Ctx, op string, err error) error {
	code := entity.CodeOf(err)
	if code == entity.CodeInternal {
		r.l.Error("restapi - v1 - %s: %v", op, err)

		return errorJSON(ctx, http.StatusInternalServerError, code, "internal server error", nil)
	}

	return errorJSON(ctx, statusOf(code), code, rootMessage(err), entity.DetailsOf(err))
}

func rootMessage(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}

		err = next
	}
}

// caller returns the authenticated user ("" for a guest on an optional route).
func caller(ctx *fiber.Ctx) string {
	id, _ := middleware.UserID(ctx)

	return id
}

func requireCaller(ctx *fiber.Ctx) (string, error) {
	id, ok := middleware.UserID(ctx)
	if !ok {
		return "", errorJSON(ctx, http.StatusUnauthorized, entity.CodeUnauthorized, "unauthorized", nil)
	}

	return id, nil
}

// idParam returns the lower-cased uuid of the path parameter {id}; ok is false when it is malformed.
func idParam(ctx *fiber.Ctx) (string, bool) {
	u, err := uuid.Parse(ctx.Params("id"))
	if err != nil {
		return "", false
	}

	return u.String(), true
}

// refuseSuspendedWrites answers 403 CHANNEL_SUSPENDED to every Studio change of a suspended channel; reading
// the Studio stays open. A caller without a channel goes on to the handler's NOT_CREATOR.
func (r *routes) refuseSuspendedWrites(ctx *fiber.Ctx) error {
	if ctx.Method() == fiber.MethodGet || ctx.Method() == fiber.MethodHead {
		return ctx.Next()
	}

	userID, ok := middleware.UserID(ctx)
	if !ok {
		return ctx.Next()
	}

	ch, err := r.uc.Studio.Channel(ctx.UserContext(), userID)
	if err == nil && ch.Suspended() {
		return r.fail(ctx, "studio", entity.ErrSuspended)
	}

	return ctx.Next()
}
