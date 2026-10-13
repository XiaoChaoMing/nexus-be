// Package restapi wires the public HTTP routes of stream-svc.
package restapi

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	_ "github.com/minhnn/nexuslive-be/services/stream/docs" // Swagger spec.
	"github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/middleware"
	v1 "github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

// NewRouter -.
// Swagger spec (make nxl-swag), served at /swagger/index.html:
//
//	@title                      NexusLive stream-svc API
//	@description                Stream keys, Studio, live catalog and VODs. Get a token from identity-svc (http://localhost:8090/swagger/index.html → /auth/login) and paste "Bearer <accessToken>" into Authorize.
//	@version                    1.0
//	@BasePath                   /v1
//	@securityDefinitions.apikey BearerAuth
//	@in                         header
//	@name                       Authorization
func NewRouter(app *fiber.App, studio usecase.Studio, catalog usecase.Catalog, viewer usecase.Viewer, verifier *jwt.JWKSVerifier,
	l logger.Interface, o v1.Options, swaggerEnabled bool,
) {
	app.Use(recover.New())

	if swaggerEnabled {
		app.Get("/swagger/*", swagger.HandlerDefault)
	}

	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	auth := v1.Auth{Required: middleware.Auth(verifier), Optional: middleware.OptionalAuth(verifier)}
	v1.NewRoutes(app.Group("/v1"), studio, catalog, viewer, auth, l, o)
}
