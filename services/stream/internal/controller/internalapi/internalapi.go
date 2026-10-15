// Package internalapi serves media-svc on the internal port (never exposed through the gateway).
package internalapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

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
	CodeChannelSuspended = "CHANNEL_SUSPENDED"
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

// Stream is one stream of GET /internal/v1/streams.
type Stream struct {
	ID         string     `json:"id"`
	ChannelID  string     `json:"channelId"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	EndReason  *string    `json:"endReason"`
	VODID      *string    `json:"vodId"`
	VODDeleted bool       `json:"vodDeleted"`
	StartedAt  *time.Time `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt"`
}

// StreamPage -.
type StreamPage struct {
	Items     []Stream `json:"items"`
	NextAfter *string  `json:"nextAfter"`
}

// NewRouter -.
func NewRouter(app *fiber.App, ingest usecase.Ingest, snap usecase.Snapshot, secret string, l logger.Interface) {
	app.Use(recover.New())
	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	g := app.Group("/internal/v1", func(ctx *fiber.Ctx) error {
		if subtle.ConstantTimeCompare([]byte(ctx.Get(SecretHeader)), []byte(secret)) != 1 {
			return fail(ctx, http.StatusUnauthorized, CodeUnauthorized)
		}

		return ctx.Next()
	})

	g.Post("/ingest/authorize", authorize(ingest, l))
	g.Get("/streams", streams(snap, l))
}

func authorize(ingest usecase.Ingest, l logger.Interface) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
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
		case errors.Is(err, entity.ErrChannelSuspended):
			return fail(ctx, http.StatusForbidden, CodeChannelSuspended)
		case err != nil:
			l.Error("internalapi - authorize: %v", err)

			return fail(ctx, http.StatusInternalServerError, CodeInternal)
		}

		return ctx.JSON(AuthorizeResponse{StreamID: grant.StreamID, ChannelID: grant.ChannelID, Resumed: grant.Resumed})
	}
}

func streams(snap usecase.Snapshot, l logger.Interface) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		items, next, err := snap.Streams(ctx.UserContext(), ctx.Query("after"), ctx.QueryInt("limit"))

		switch {
		case errors.Is(err, entity.ErrInvalidCursor):
			return fail(ctx, http.StatusBadRequest, CodeValidation)
		case err != nil:
			l.Error("internalapi - streams: %v", err)

			return fail(ctx, http.StatusInternalServerError, CodeInternal)
		}

		out := StreamPage{Items: make([]Stream, 0, len(items))}
		if next != "" {
			out.NextAfter = &next
		}

		for _, s := range items {
			out.Items = append(out.Items, Stream{
				ID: s.ID, ChannelID: s.ChannelID, Title: s.Title, Status: string(s.Status), EndReason: s.EndReason,
				VODID: s.VODID, VODDeleted: s.VODDeleted, StartedAt: utc(s.StartedAt), EndedAt: utc(s.EndedAt),
			})
		}

		return ctx.JSON(out)
	}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	u := t.UTC()

	return &u
}

func fail(ctx *fiber.Ctx, status int, code string) error {
	return ctx.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code}})
}
