package v1

import (
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

const (
	// RefreshCookie holds the refresh token; HttpOnly so scripts on the page can never read it.
	RefreshCookie = "nxl_rt"
	_cookiePath   = "/v1/auth"
)

// register godoc
//
//	@Summary     Register
//	@Description Creates an account and signs in: returns an access token and sets the refresh cookie nxl_rt.
//	@Description acceptTerms must be true. A verification e-mail is sent (auth.email_requested.v1).
//	@Tags        auth
//	@Accept      json
//	@Produce     json
//	@Param       body body     request.Register true "New account"
//	@Success     201  {object} response.Data[response.Session]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (details.acceptTerms = REQUIRED, ...)"
//	@Failure     409  {object} response.Error "USERNAME_TAKEN / EMAIL_TAKEN (details.<field> = TAKEN)"
//	@Router      /auth/register [post]
func (r *V1) register(ctx *fiber.Ctx) error {
	var body request.Register
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	res, err := r.auth.Register(ctx.UserContext(), usecase.RegisterInput{
		Username:    body.Username,
		DisplayName: body.DisplayName,
		Email:       body.Email,
		Password:    body.Password,
		Client:      clientInfo(ctx),
	})
	if err != nil {
		return r.mapError(ctx, "register", err)
	}

	return r.session(ctx, http.StatusCreated, res)
}

// login godoc
//
//	@Summary     Login
//	@Description Login with email or username. Copy data.accessToken into Authorize (Bearer) to call protected endpoints.
//	@Tags        auth
//	@Accept      json
//	@Produce     json
//	@Param       body body     request.Login true "Credentials"
//	@Success     200  {object} response.Data[response.Session]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED"
//	@Failure     401  {object} response.Error "INVALID_CREDENTIALS"
//	@Failure     403  {object} response.Error "ACCOUNT_LOCKED (details.lockedUntil when the lock is temporary)"
//	@Router      /auth/login [post]
func (r *V1) login(ctx *fiber.Ctx) error {
	var body request.Login
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	res, err := r.auth.Login(ctx.UserContext(), usecase.LoginInput{
		Login:    body.Login,
		Password: body.Password,
		Client:   clientInfo(ctx),
	})
	if err != nil {
		return r.mapError(ctx, "login", err)
	}

	return r.session(ctx, http.StatusOK, res)
}

// refresh godoc
//
//	@Summary     Refresh the access token
//	@Description Uses the HttpOnly cookie nxl_rt (sent automatically by the browser) and rotates it.
//	@Description The X-NXL-Client header (web or admin) is required as a CSRF guard; Swagger UI cannot send the
//	@Description SameSite=Strict cookie cross-origin, so try this from the web app.
//	@Tags        auth
//	@Produce     json
//	@Param       X-NXL-Client header   string true "Calling app (CSRF guard)" Enums(web, admin)
//	@Success     200          {object} response.Data[response.Session]
//	@Failure     401          {object} response.Error "SESSION_EXPIRED"
//	@Failure     403          {object} response.Error "FORBIDDEN (details.header = REQUIRED) / ACCOUNT_LOCKED (details.lockedUntil when the lock has an end)"
//	@Router      /auth/refresh [post]
func (r *V1) refresh(ctx *fiber.Ctx) error {
	res, err := r.auth.Refresh(ctx.UserContext(), ctx.Cookies(RefreshCookie), clientInfo(ctx))
	if err != nil {
		return r.mapError(ctx, "refresh", err)
	}

	return r.session(ctx, http.StatusOK, res)
}

// logout godoc
//
//	@Summary     Logout
//	@Description Revokes the refresh session in the cookie and clears it. Requires the X-NXL-Client header (CSRF guard).
//	@Tags        auth
//	@Param       X-NXL-Client header string true "Calling app (CSRF guard)" Enums(web, admin)
//	@Success     204
//	@Failure     403 {object} response.Error "FORBIDDEN (details.header = REQUIRED)"
//	@Router      /auth/logout [post]
func (r *V1) logout(ctx *fiber.Ctx) error {
	err := r.auth.Logout(ctx.UserContext(), ctx.Cookies(RefreshCookie))

	r.clearRefreshCookie(ctx)

	if err != nil {
		return r.mapError(ctx, "logout", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// me godoc
//
//	@Summary     Current user
//	@Description Kept for compatibility; GET /me returns the same user with roles and preferences.
//	@Tags     auth
//	@Produce  json
//	@Security BearerAuth
//	@Success  200 {object} response.Data[response.User]
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Router   /auth/me [get]
func (r *V1) me(ctx *fiber.Ctx) error {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	user, err := r.auth.Me(ctx.UserContext(), userID)
	if err != nil {
		return r.mapError(ctx, "me", err)
	}

	return ctx.JSON(response.Data[response.User]{Data: response.NewUser(user)})
}

// jwksHandler godoc
//
//	@Summary     JWKS
//	@Description Public keys other services use to verify access tokens locally.
//	@Tags        auth
//	@Produce     json
//	@Success     200 {object} jwt.JWKS
//	@Router      /auth/.well-known/jwks.json [get]
func (r *V1) jwksHandler(ctx *fiber.Ctx) error {
	ctx.Set(fiber.HeaderCacheControl, "public, max-age=300")

	return ctx.JSON(r.jwks)
}

func (r *V1) session(ctx *fiber.Ctx, status int, res usecase.AuthResult) error {
	ctx.Cookie(&fiber.Cookie{
		Name:     RefreshCookie,
		Value:    res.RefreshToken,
		Path:     _cookiePath,
		Expires:  res.RefreshExpiresAt,
		HTTPOnly: true,
		Secure:   r.cookieSecure,
		SameSite: fiber.CookieSameSiteStrictMode,
	})

	return ctx.Status(status).JSON(response.Data[response.Session]{Data: response.Session{
		AccessToken: res.AccessToken,
		ExpiresIn:   int(res.AccessExpiresIn / time.Second),
		User:        response.NewUser(res.User),
	}})
}

func (r *V1) clearRefreshCookie(ctx *fiber.Ctx) {
	ctx.Cookie(&fiber.Cookie{
		Name:     RefreshCookie,
		Value:    "",
		Path:     _cookiePath,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   r.cookieSecure,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
}

func clientInfo(ctx *fiber.Ctx) usecase.ClientInfo {
	return usecase.ClientInfo{UserAgent: ctx.Get(fiber.HeaderUserAgent), IP: ctx.IP()}
}
