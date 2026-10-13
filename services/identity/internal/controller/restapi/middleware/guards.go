package middleware

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
)

const (
	// ClientHeader must accompany cookie-authenticated calls (refresh, logout). A cross-site page cannot
	// set a custom header without a CORS preflight the edge never grants, so the header blocks CSRF.
	ClientHeader = "X-NXL-Client"

	_clientWeb   = "web"
	_clientAdmin = "admin"
	_roleAdmin   = "ADMIN"
)

// RequireClient refuses requests without X-NXL-Client: web|admin with 403 FORBIDDEN.
func RequireClient() fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		switch ctx.Get(ClientHeader) {
		case _clientWeb, _clientAdmin:
			return ctx.Next()
		default:
			return ctx.Status(http.StatusForbidden).JSON(fiber.Map{
				"error": fiber.Map{
					"code":    "FORBIDDEN",
					"message": "missing client header",
					"details": fiber.Map{"header": "REQUIRED"},
				},
			})
		}
	}
}

// RequireAdmin runs after Auth and lets only access tokens with role ADMIN through. The role comes from
// the token, so a promotion takes effect at the next refresh.
func RequireAdmin() fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		if Role(ctx) != _roleAdmin {
			return ctx.Status(http.StatusForbidden).JSON(fiber.Map{
				"error": fiber.Map{"code": "FORBIDDEN", "message": "admin only"},
			})
		}

		return ctx.Next()
	}
}
