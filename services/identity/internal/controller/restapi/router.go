// Package restapi wires HTTP routes for identity-svc.
package restapi

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"
	_ "github.com/minhnn/nexuslive-be/services/identity/docs" // Swagger spec.
	v1 "github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1"
)

// NewRouter -.
// Swagger spec (make nxl-swag), served at /swagger/index.html:
//
//	@title                      NexusLive identity-svc API
//	@description                Accounts, sessions, account self-service and creator applications. Access tokens are EdDSA
//	@description                JWTs; other services verify them with the JWKS. /admin routes need an access token with role ADMIN.
//	@version                    1.0
//	@BasePath                   /v1
//	@securityDefinitions.apikey BearerAuth
//	@in                         header
//	@name                       Authorization
//	@description                "Bearer " + data.accessToken from /auth/login
func NewRouter(app *fiber.App, deps v1.Deps, swaggerEnabled bool) {
	app.Use(recover.New())

	if swaggerEnabled {
		app.Get("/swagger/*", swagger.HandlerDefault)
	}

	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	v1.NewRoutes(app.Group("/v1"), deps)
}
