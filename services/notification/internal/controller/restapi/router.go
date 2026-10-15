// Package restapi wires the public HTTP routes of notification-svc.
package restapi

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	_ "github.com/minhnn/nexuslive-be/services/notification/docs" // Swagger spec.
	"github.com/minhnn/nexuslive-be/services/notification/internal/controller/restapi/middleware"
	v1 "github.com/minhnn/nexuslive-be/services/notification/internal/controller/restapi/v1"
)

// NewRouter -.
// Swagger spec (make nxl-swag), served at /swagger/index.html:
//
//	@title                      NexusLive notification-svc API
//	@description                The notification center (list, unread count, read, delete), notification settings and e-mail unsubscribe. Realtime delivery goes through chat-svc on the WebSocket topic user:{userId}. Get a token from identity-svc (http://localhost:8090/swagger/index.html → /auth/login) and paste "Bearer <accessToken>" into Authorize.
//	@version                    1.0
//	@BasePath                   /v1
//	@securityDefinitions.apikey BearerAuth
//	@in                         header
//	@name                       Authorization
func NewRouter(app *fiber.App, uc v1.UseCases, verifier *jwt.JWKSVerifier, l logger.Interface, swaggerEnabled bool) {
	app.Use(recover.New())

	if swaggerEnabled {
		app.Get("/swagger/*", swagger.HandlerDefault)
	}

	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	v1.NewRoutes(app.Group("/v1"), uc, middleware.Auth(verifier), l)
}
