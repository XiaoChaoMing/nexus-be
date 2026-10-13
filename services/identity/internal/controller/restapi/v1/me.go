package v1

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

func (r *V1) caller(ctx *fiber.Ctx) (string, bool) {
	return middleware.UserID(ctx)
}

// getMe godoc
//
//	@Summary     Signed-in account
//	@Description The user plus roles (always VIEWER; CREATOR with a channel; ADMIN), channel and preferences.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} response.Data[response.Me]
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Router      /me [get]
func (r *V1) getMe(ctx *fiber.Ctx) error {
	userID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	p, err := r.auth.Profile(ctx.UserContext(), userID)
	if err != nil {
		return r.mapError(ctx, "getMe", err)
	}

	return ctx.JSON(response.Data[response.Me]{Data: response.NewMe(p)})
}

// updateMe godoc
//
//	@Summary     Update the account
//	@Description Changes only the fields present. displayName 1-50 characters (trimmed); locale vi or en;
//	@Description timezone an IANA name; birthDate YYYY-MM-DD, not in the future, only while it is empty.
//	@Tags        me
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     request.UpdateMe true "Fields to change"
//	@Success     200  {object} response.Data[response.Me]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (details.birthDate = ALREADY_SET / INVALID, ...)"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Router      /me [patch]
func (r *V1) updateMe(ctx *fiber.Ctx) error {
	userID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	var body request.UpdateMe
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if body.DisplayName != nil {
		name := strings.TrimSpace(*body.DisplayName)
		body.DisplayName = &name
	}

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	in := usecase.ProfileUpdate{
		DisplayName: body.DisplayName, BirthDate: body.BirthDate, Locale: body.Locale, Timezone: body.Timezone,
	}

	if p := body.Preferences; p != nil {
		in.Preferences = &usecase.PreferencesUpdate{SidebarCollapsed: p.SidebarCollapsed, MatureVisible: p.MatureVisible}
	}

	p, err := r.auth.UpdateProfile(ctx.UserContext(), userID, in)
	if err != nil {
		return r.mapError(ctx, "updateMe", err)
	}

	return ctx.JSON(response.Data[response.Me]{Data: response.NewMe(p)})
}

// changePassword godoc
//
//	@Summary     Change the password
//	@Description Signs out every other session and returns a new one (new access token and nxl_rt cookie).
//	@Tags        me
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     request.ChangePassword true "Current and new password"
//	@Success     200  {object} response.Data[response.Session]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED / WRONG_PASSWORD"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Router      /me/password [post]
func (r *V1) changePassword(ctx *fiber.Ctx) error {
	userID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	var body request.ChangePassword
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	res, err := r.auth.ChangePassword(ctx.UserContext(), usecase.ChangePasswordInput{
		UserID: userID, CurrentPassword: body.CurrentPassword, NewPassword: body.NewPassword, Client: clientInfo(ctx),
	})
	if err != nil {
		return r.mapError(ctx, "changePassword", err)
	}

	return r.session(ctx, http.StatusOK, res)
}
