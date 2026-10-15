// Package restapi wires the HTTP routes of chat-svc: the REST API and the WebSocket gateway share one port.
package restapi

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	_ "github.com/minhnn/nexuslive-be/services/chat/docs" // Swagger spec.
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/middleware"
	v1 "github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1"
)

// NewRouter -.
// Swagger spec (make nxl-swag), served at /swagger/index.html:
//
//	@title                      NexusLive chat-svc API
//	@description                Chat history, emotes, chat modes and moderation (delete, pin, timeout, ban, blocked terms, moderator / VIP roles). The realtime gateway (WebSocket GET /v1/ws) is described in docs/plans/technical/2026-10-14-gd03-chat-realtime.md §5.1. Get a token from identity-svc and paste "Bearer <accessToken>" into Authorize.
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

	v1.NewRoutes(app.Group("/v1"), uc, v1.Auth{Required: middleware.Auth(verifier), Optional: middleware.OptionalAuth(verifier)}, l)
}
