// Package internalapi serves media-svc on the internal port (never exposed through the gateway).
package internalapi

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

// SecretHeader carries the shared secret between internal services.
const SecretHeader = "X-Internal-Secret" //nolint:gosec // a header name, not a secret

// Error codes understood by media-svc.
const (
	CodeInvalidStreamKey = "INVALID_STREAM_KEY"
	CodeAlreadyLive      = "ALREADY_LIVE"
	CodeRecentlyEnded    = "RECENTLY_ENDED"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeValidation       = "VALIDATION_FAILED"
	CodeInternal         = "INTERNAL_ERROR"
)

// AuthorizeRequest -.
type AuthorizeRequest struct {
	StreamKey string `json:"streamKey"`
	ClientIP  string `json:"clientIp"`
}

// AuthorizeResponse -.
type AuthorizeResponse struct {
	StreamID  string `json:"streamId"`
	ChannelID string `json:"channelId"`
	Resumed   bool   `json:"resumed"`
}

// NewRouter -.
func NewRouter(app *fiber.App, ingest usecase.Ingest, secret string, l logger.Interface) {
	app.Use(recover.New())
	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	g := app.Group("/internal/v1", func(ctx *fiber.Ctx) error {
		if subtle.ConstantTimeCompare([]byte(ctx.Get(SecretHeader)), []byte(secret)) != 1 {
			return fail(ctx, http.StatusUnauthorized, CodeUnauthorized)
		}

		return ctx.Next()
	})

	g.Post("/ingest/authorize", func(ctx *fiber.Ctx) error {
		var req AuthorizeRequest
		if err := ctx.BodyParser(&req); err != nil || req.StreamKey == "" {
			return fail(ctx, http.StatusBadRequest, CodeValidation)
		}

		grant, err := ingest.Authorize(ctx.UserContext(), req.StreamKey, req.ClientIP)

		switch {
		case errors.Is(err, entity.ErrInvalidStreamKey):
			return fail(ctx, http.StatusForbidden, CodeInvalidStreamKey)
		case errors.Is(err, entity.ErrAlreadyLive):
			return fail(ctx, http.StatusConflict, CodeAlreadyLive)
		case errors.Is(err, entity.ErrRecentlyEnded):
			return fail(ctx, http.StatusConflict, CodeRecentlyEnded)
		case err != nil:
			l.Error("internalapi - authorize: %v", err)

			return fail(ctx, http.StatusInternalServerError, CodeInternal)
		}

		return ctx.JSON(AuthorizeResponse{StreamID: grant.StreamID, ChannelID: grant.ChannelID, Resumed: grant.Resumed})
	})
}

func fail(ctx *fiber.Ctx, status int, code string) error {
	return ctx.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code}})
}
