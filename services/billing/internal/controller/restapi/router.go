// Package restapi wires the HTTP routes of billing-svc.
package restapi

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	_ "github.com/minhnn/nexuslive-be/services/billing/docs" // Swagger spec.
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/middleware"
	v1 "github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/v1"
)

// NewRouter -.
// Swagger spec (make nxl-swag), served at /swagger/index.html:
//
//	@title                      NexusLive billing-svc API
//	@description                Subscription tiers, checkout (SePay Payment Gateway or the dev fake), the SePay IPN webhook, the viewer's subscriptions and payments, Studio monetization (tiers, overview, ledger, payouts) and the admin payout queue. Get a token from identity-svc (http://localhost:8090/swagger/index.html → /auth/login; admin@nexuslive.dev for /admin) and paste "Bearer <accessToken>" into Authorize.
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

	v1.NewRoutes(app.Group("/v1"), uc, middleware.Auth(verifier), middleware.RequireAdmin(), l)
}
