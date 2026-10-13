package v1

import (
	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

// Deps are what the v1 routes need.
type Deps struct {
	Auth         usecase.Auth
	Creator      usecase.Creator
	Signer       *jwt.EdDSA
	Logger       logger.Interface
	CookieSecure bool
}

// NewRoutes registers every /v1 route of identity-svc.
func NewRoutes(group fiber.Router, d Deps) {
	r := &V1{auth: d.Auth, creator: d.Creator, jwks: d.Signer.JWKS(), l: d.Logger, v: NewValidator(), cookieSecure: d.CookieSecure}
	authn := middleware.Auth(d.Signer)

	auth := group.Group("/auth")
	{
		auth.Post("/register", r.register)
		auth.Post("/login", r.login)
		auth.Post("/refresh", middleware.RequireClient(), r.refresh)
		auth.Post("/logout", middleware.RequireClient(), r.logout)
		auth.Get("/me", authn, r.me)
		auth.Get("/.well-known/jwks.json", r.jwksHandler)
		auth.Post("/verify-email", r.verifyEmail)
		auth.Post("/verify-email/resend", authn, r.resendVerification)
		auth.Post("/password/forgot", r.forgotPassword)
		auth.Post("/password/reset", r.resetPassword)
	}

	me := group.Group("/me")
	{
		me.Get("", authn, r.getMe)
		me.Patch("", authn, r.updateMe)
		me.Post("/password", authn, r.changePassword)
	}

	apps := group.Group("/creator-applications")
	{
		apps.Post("", authn, r.submitApplication)
		apps.Get("/me", authn, r.myApplication)
		apps.Delete("/:id", authn, r.cancelApplication)
	}

	admin := group.Group("/admin/creator-applications")
	adminOnly := middleware.RequireAdmin()
	{
		admin.Get("", authn, adminOnly, r.listApplications)
		admin.Get("/:id", authn, adminOnly, r.getApplication)
		admin.Post("/:id/approve", authn, adminOnly, r.approveApplication)
		admin.Post("/:id/reject", authn, adminOnly, r.rejectApplication)
	}
}
