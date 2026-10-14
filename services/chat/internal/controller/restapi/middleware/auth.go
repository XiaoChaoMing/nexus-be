package middleware

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
)

const (
	_localUserID = "userID"
	_localRole   = "role"
	_bearer      = "Bearer "
	_jwtDots     = 2
)

type verifier interface {
	Verify(token string) (*jwt.Claims, error)
}

// Auth verifies the Bearer access token locally (no call to identity storage) and stores the caller in Locals.
func Auth(v verifier) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		token, ok := bearerToken(ctx.Get(fiber.HeaderAuthorization))
		if !ok {
			return unauthorized(ctx)
		}

		claims, err := v.Verify(token)
		if err != nil {
			return unauthorized(ctx)
		}

		ctx.Locals(_localUserID, claims.Subject)
		ctx.Locals(_localRole, claims.Role)

		return ctx.Next()
	}
}

// OptionalAuth lets guests through and identifies signed-in callers. A token that is sent but invalid is
// still rejected with 401, so the client refreshes instead of silently watching as a guest.
func OptionalAuth(v verifier) fiber.Handler {
	required := Auth(v)

	return func(ctx *fiber.Ctx) error {
		if strings.TrimSpace(ctx.Get(fiber.HeaderAuthorization)) == "" {
			return ctx.Next()
		}

		return required(ctx)
	}
}

// bearerToken accepts "Bearer <jwt>" (any case) and, for Swagger UI's API-key dialog where people
// paste the token alone, a bare JWT. The token is verified either way.
func bearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if len(header) > len(_bearer) && strings.EqualFold(header[:len(_bearer)], _bearer) {
		header = strings.TrimSpace(header[len(_bearer):])
	}

	if strings.Count(header, ".") != _jwtDots || strings.ContainsAny(header, " \t") {
		return "", false
	}

	return header, true
}

// Role returns the global role of the authenticated user ("" when unknown).
func Role(ctx *fiber.Ctx) string {
	role, _ := ctx.Locals(_localRole).(string) //nolint:errcheck // "" when missing

	return role
}

// UserID returns the authenticated user id set by Auth.
func UserID(ctx *fiber.Ctx) (string, bool) {
	id, ok := ctx.Locals(_localUserID).(string)

	return id, ok && id != ""
}

func unauthorized(ctx *fiber.Ctx) error {
	return ctx.Status(http.StatusUnauthorized).JSON(fiber.Map{
		"error": fiber.Map{"code": "UNAUTHORIZED", "message": "unauthorized"},
	})
}
