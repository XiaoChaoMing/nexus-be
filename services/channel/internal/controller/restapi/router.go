// Package restapi wires the public HTTP routes of channel-svc.
package restapi

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/swagger"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	_ "github.com/minhnn/nexuslive-be/services/channel/docs" // Swagger spec.
	v1 "github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

// NewRouter -.
// Swagger spec (make nxl-swag), served at /swagger/index.html:
//
//	@title       NexusLive channel-svc API
//	@description Public channel pages. Channels are created by channel-svc when an admin approves a creator application.
//	@version     1.0
//	@BasePath    /v1
func NewRouter(app *fiber.App, dir usecase.Directory, l logger.Interface, swaggerEnabled bool) {
	app.Use(recover.New())

	if swaggerEnabled {
		app.Get("/swagger/*", swagger.HandlerDefault)
	}

	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	v1.NewRoutes(app.Group("/v1"), dir, l)
}
