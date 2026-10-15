package v1

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

// getSettings -.
//
//	@Summary     My notification settings
//	@Description The 8 types in a fixed order. Locked switches (CREATOR_APPLICATION, SYSTEM) cannot change; NEW_FOLLOWER has no e-mail. emailVerified=false means no e-mail is sent until the address is verified.
//	@Tags        notification-settings
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} Data[Settings]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Router      /me/notification-settings [get]
func (r *routes) getSettings(ctx *fiber.Ctx) error {
	userID, err := caller(ctx)
	if err != nil {
		return err
	}

	v, err := r.uc.Settings.Get(ctx.UserContext(), userID)
	if err != nil {
		return r.fail(ctx, "getSettings", err)
	}

	return ctx.JSON(Data[Settings]{Data: settingsOf(v)})
}

// putSettings -.
//
//	@Summary     Change my notification settings
//	@Description Send only the rows that change (1–8); an omitted switch keeps its value. Turning a locked switch off or e-mail on for NEW_FOLLOWER → 400 details.<TYPE>=INVALID. Returns the whole table.
//	@Tags        notification-settings
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     SettingsRequest true "rows to change"
//	@Success     200  {object} Data[Settings]
//	@Failure     400  {object} ErrorResponse "VALIDATION_FAILED (items=REQUIRED, type=ENUM, <TYPE>=INVALID)"
//	@Failure     401  {object} ErrorResponse "UNAUTHORIZED"
//	@Router      /me/notification-settings [put]
func (r *routes) putSettings(ctx *fiber.Ctx) error {
	userID, err := caller(ctx)
	if err != nil {
		return err
	}

	var body SettingsRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{"items": entity.DetailRequired})
	}

	patches := make([]usecase.SettingPatch, 0, len(body.Items))
	for _, it := range body.Items {
		patches = append(patches, usecase.SettingPatch{Type: it.Type, InApp: it.InApp, Email: it.Email})
	}

	v, err := r.uc.Settings.Update(ctx.UserContext(), userID, patches)
	if err != nil {
		return r.fail(ctx, "putSettings", err)
	}

	return ctx.JSON(Data[Settings]{Data: settingsOf(v)})
}

// unsubscribe -.
//
//	@Summary     Turn off one type of e-mail from the link of an e-mail (no sign-in)
//	@Description The token comes from {WEB_BASE_URL}/notifications/unsubscribe?token=…; it never expires. A malformed or forged token → 400 TOKEN_INVALID.
//	@Tags        notification-settings
//	@Accept      json
//	@Produce     json
//	@Param       body body     UnsubscribeRequest true "token"
//	@Success     200  {object} Data[Unsubscribed]
//	@Failure     400  {object} ErrorResponse "TOKEN_INVALID, VALIDATION_FAILED (token=REQUIRED)"
//	@Router      /notifications/unsubscribe [post]
func (r *routes) unsubscribe(ctx *fiber.Ctx) error {
	var body UnsubscribeRequest
	if err := ctx.BodyParser(&body); err != nil || strings.TrimSpace(body.Token) == "" {
		return validationFailed(ctx, map[string]string{"token": entity.DetailRequired})
	}

	t, err := r.uc.Settings.Unsubscribe(ctx.UserContext(), strings.TrimSpace(body.Token))
	if err != nil {
		return r.fail(ctx, "unsubscribe", err)
	}

	return ctx.JSON(Data[Unsubscribed]{Data: Unsubscribed{Type: string(t)}})
}
