// Package v1 is the public REST API of channel-svc.
package v1

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

// Error codes shared with the web client.
const (
	CodeNotFound         = "NOT_FOUND"
	CodeChannelSuspended = "CHANNEL_SUSPENDED"
	CodeInternal         = "INTERNAL_ERROR"
)

type routes struct {
	dir usecase.Directory
	l   logger.Interface
}

// NewRoutes registers /v1/channels.
func NewRoutes(group fiber.Router, dir usecase.Directory, l logger.Interface) {
	r := &routes{dir: dir, l: l}

	group.Get("/channels/:handle", r.byHandle)
}

func errorJSON(ctx *fiber.Ctx, status int, code, message string) error {
	return ctx.Status(status).JSON(ErrorResponse{Error: ErrorBody{Code: code, Message: message}})
}

// byHandle godoc
//
//	@Summary     Public channel page by handle
//	@Description The handle is case-insensitive (3–25 characters of [A-Za-z0-9_]); anything else is 404.
//	@Tags        channels
//	@Produce     json
//	@Param       handle path     string true "channel handle"
//	@Success     200    {object} Data[Channel]
//	@Failure     403    {object} ErrorResponse "CHANNEL_SUSPENDED"
//	@Failure     404    {object} ErrorResponse "NOT_FOUND"
//	@Router      /channels/{handle} [get]
func (r *routes) byHandle(ctx *fiber.Ctx) error {
	ch, err := r.dir.ByHandle(ctx.UserContext(), ctx.Params("handle"))

	switch {
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "channel not found")
	case errors.Is(err, entity.ErrSuspended):
		return errorJSON(ctx, http.StatusForbidden, CodeChannelSuspended, "channel is suspended")
	case err != nil:
		r.l.Error("restapi - v1 - byHandle: %v", err)

		return errorJSON(ctx, http.StatusInternalServerError, CodeInternal, "internal server error")
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(Data[Channel]{Data: Channel{
		ID:              ch.ID,
		Handle:          ch.Handle,
		DisplayName:     ch.DisplayName,
		Bio:             ch.Bio,
		AvatarURL:       ch.AvatarURL,
		IsLive:          ch.IsLive,
		CurrentStreamID: ch.CurrentStreamID,
		CreatedAt:       ch.CreatedAt.UTC(),
	}})
}
