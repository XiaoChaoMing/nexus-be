// Package restapi wires the public HTTP routes of channel-svc.
package restapi

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	_ "github.com/minhnn/nexuslive-be/services/channel/docs" // Swagger spec.
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/middleware"
	v1 "github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1"
)

// NewRouter -.
// Swagger spec (make nxl-swag), served at /swagger/index.html:
//
//	@title                      NexusLive channel-svc API
//	@description                Channels, follows, categories, search and broadcast schedules. Channels are created when an admin approves a creator application. Get a token from identity-svc and paste "Bearer <accessToken>" into Authorize.
//	@version                    1.1
//	@BasePath                   /v1
//	@securityDefinitions.apikey BearerAuth
//	@in                         header
//	@name                       Authorization
func NewRouter(app *fiber.App, uc v1.UseCases, verifier *jwt.JWKSVerifier, o v1.Options, l logger.Interface, swaggerEnabled bool) {
	app.Use(recover.New())

	if swaggerEnabled {
		app.Get("/swagger/*", swagger.HandlerDefault)
	}

	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	auth := v1.Auth{Required: middleware.Auth(verifier), Optional: middleware.OptionalAuth(verifier)}
	v1.NewRoutes(app.Group("/v1"), uc, auth, o, l)
}
