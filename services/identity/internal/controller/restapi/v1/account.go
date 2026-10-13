package v1

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/response"
)

// accepted answers 202 with an empty body (SendStatus would write the status text).
func accepted(ctx *fiber.Ctx) error {
	ctx.Status(http.StatusAccepted)

	return nil
}

// verifyEmail godoc
//
//	@Summary     Verify the e-mail address
//	@Description Consumes the one-time token from the verification e-mail ({web}/verify-email?token=...).
//	@Tags        auth
//	@Accept      json
//	@Produce     json
//	@Param       body body     request.VerifyEmail true "Token from the e-mail link"
//	@Success     200  {object} response.Data[response.User]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED / TOKEN_INVALID (unknown, expired or used)"
//	@Router      /auth/verify-email [post]
func (r *V1) verifyEmail(ctx *fiber.Ctx) error {
	var body request.VerifyEmail
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	user, err := r.auth.VerifyEmail(ctx.UserContext(), body.Token)
	if err != nil {
		return r.mapError(ctx, "verifyEmail", err)
	}

	return ctx.JSON(response.Data[response.User]{Data: response.NewUser(user)})
}

// resendVerification godoc
//
//	@Summary     Resend the verification e-mail
//	@Description Issues a new token (older links stop working). At most one per 60 s and 5 per hour.
//	@Tags        auth
//	@Produce     json
//	@Security    BearerAuth
//	@Success     202
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Failure     409 {object} response.Error "CONFLICT (already verified)"
//	@Failure     429 {object} response.Error "RATE_LIMITED (details.retryAfterSec, header Retry-After)"
//	@Router      /auth/verify-email/resend [post]
func (r *V1) resendVerification(ctx *fiber.Ctx) error {
	userID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	if err := r.auth.ResendVerification(ctx.UserContext(), userID); err != nil {
		return r.mapError(ctx, "resendVerification", err)
	}

	return accepted(ctx)
}

// forgotPassword godoc
//
//	@Summary     Request a password reset e-mail
//	@Description Always 202, whether or not the e-mail is registered (and when rate limited), so accounts
//	@Description cannot be discovered. The link ({web}/reset-password?token=...) is valid for 30 minutes.
//	@Tags        auth
//	@Accept      json
//	@Param       body body request.ForgotPassword true "Account e-mail"
//	@Success     202
//	@Failure     400 {object} response.Error "VALIDATION_FAILED"
//	@Router      /auth/password/forgot [post]
func (r *V1) forgotPassword(ctx *fiber.Ctx) error {
	var body request.ForgotPassword
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	if err := r.auth.ForgotPassword(ctx.UserContext(), body.Email); err != nil {
		return r.mapError(ctx, "forgotPassword", err)
	}

	return accepted(ctx)
}

// resetPassword godoc
//
//	@Summary     Reset the password
//	@Description Consumes the reset token, stores the new password, signs out every session and lifts a
//	@Description temporary lock.
//	@Tags        auth
//	@Accept      json
//	@Param       body body request.ResetPassword true "Token from the e-mail link and the new password"
//	@Success     204
//	@Failure     400 {object} response.Error "VALIDATION_FAILED / TOKEN_INVALID"
//	@Router      /auth/password/reset [post]
func (r *V1) resetPassword(ctx *fiber.Ctx) error {
	var body request.ResetPassword
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	if err := r.auth.ResetPassword(ctx.UserContext(), body.Token, body.Password); err != nil {
		return r.mapError(ctx, "resetPassword", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}
