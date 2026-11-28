package v1

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// overlaySettings godoc
//
//	@Summary     OBS overlay options of a channel
//	@Description Public: the overlay pages run in OBS without a sign-in. A channel that never saved any gets the defaults.
//	@Tags        overlay
//	@Produce     json
//	@Param       channelId path     string true "channel id (uuid)"
//	@Success     200       {object} response.Data[response.OverlaySettings]
//	@Failure     404       {object} response.Error "NOT_FOUND"
//	@Router      /channels/{channelId}/overlay-settings [get]
func (r *routes) overlaySettings(ctx *fiber.Ctx) error {
	channelID := ctx.Params("channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		return errorJSON(ctx, http.StatusNotFound, entity.CodeNotFound, "channel not found", nil)
	}

	s, err := r.overlay.Get(ctx.UserContext(), channelID)
	if err != nil {
		return r.fail(ctx, "overlaySettings", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(response.Data[response.OverlaySettings]{Data: overlayOf(s)})
}

// updateOverlaySettings godoc
//
//	@Summary     Change the OBS overlay options of a channel
//	@Description Channel owner and admins. Overlay pages that are open restyle themselves at once (overlay:{channelId} event overlay_settings).
//	@Tags        overlay
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string                 true "channel id (uuid)"
//	@Param       body      body     request.UpdateOverlay  true "chat theme and gift alert"
//	@Success     200       {object} response.Data[response.OverlaySettings]
//	@Failure     400       {object} response.Error "VALIDATION_FAILED (details.<chat|alert>.<field> = ENUM | FORMAT | LENGTH)"
//	@Failure     401       {object} response.Error "UNAUTHORIZED"
//	@Failure     403       {object} response.Error "FORBIDDEN"
//	@Failure     404       {object} response.Error "NOT_FOUND"
//	@Router      /channels/{channelId}/overlay-settings [put]
func (r *routes) updateOverlaySettings(ctx *fiber.Ctx) error {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, entity.CodeUnauthorized, "unauthorized", nil)
	}

	channelID := ctx.Params("channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		return errorJSON(ctx, http.StatusNotFound, entity.CodeNotFound, "channel not found", nil)
	}

	var body request.UpdateOverlay
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	p := entity.Principal{UserID: userID, GlobalRole: middleware.Role(ctx), IP: ctx.IP()}

	s, err := r.overlay.Update(ctx.UserContext(), p, body.Settings(channelID))
	if err != nil {
		return r.fail(ctx, "updateOverlaySettings", err)
	}

	return ctx.JSON(response.Data[response.OverlaySettings]{Data: overlayOf(s)})
}

// testOverlay godoc
//
//	@Summary     Show a test gift alert on the channel's open overlay pages
//	@Description Channel owner and admins. The alert goes to overlay pages only: not to chat, not to the ledger. At most one per second per channel.
//	@Tags        overlay
//	@Accept      json
//	@Security    BearerAuth
//	@Param       channelId path string                   true  "channel id (uuid)"
//	@Param       body      body request.TestOverlayGift  false "optional alert fields; empty ones take demo values"
//	@Success     204
//	@Failure     400 {object} response.Error "VALIDATION_FAILED"
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Failure     403 {object} response.Error "FORBIDDEN"
//	@Failure     404 {object} response.Error "NOT_FOUND"
//	@Failure     429 {object} response.Error "RATE_LIMITED"
//	@Router      /channels/{channelId}/overlay-test [post]
func (r *routes) testOverlay(ctx *fiber.Ctx) error {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, entity.CodeUnauthorized, "unauthorized", nil)
	}

	channelID := ctx.Params("channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		return errorJSON(ctx, http.StatusNotFound, entity.CodeNotFound, "channel not found", nil)
	}

	var body request.TestOverlayGift
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(&body); err != nil {
			return validationFailed(ctx, nil)
		}
	}

	if errs := body.Validate(); len(errs) > 0 {
		return validationFailed(ctx, errs)
	}

	p := entity.Principal{UserID: userID, GlobalRole: middleware.Role(ctx), IP: ctx.IP()}

	if err := r.overlay.TestGift(ctx.UserContext(), p, channelID, body.Gift()); err != nil {
		return r.fail(ctx, "testOverlay", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

func overlayOf(s entity.OverlaySettings) response.OverlaySettings {
	out := response.OverlaySettings{Chat: s.Chat, Alert: s.Alert}

	if s.UpdatedAt != nil {
		t := s.UpdatedAt.UTC()
		out.UpdatedAt = &t
	}

	return out
}
