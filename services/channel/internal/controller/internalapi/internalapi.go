// Package internalapi serves other services on the internal port (never exposed through the edge): the
// channel and follow snapshots safety-svc, notification-svc and chat-svc backfill their copies from.
package internalapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

// SecretHeader carries the shared secret between internal services.
const SecretHeader = "X-Internal-Secret" //nolint:gosec // a header name, not a secret

// Channel is one channel of GET /internal/v1/channels.
type Channel struct {
	ID          string     `json:"id"`
	OwnerID     string     `json:"ownerId"`
	Handle      string     `json:"handle"`
	DisplayName string     `json:"displayName"`
	AvatarURL   *string    `json:"avatarUrl"`
	Status      string     `json:"status"`
	SuspendedAt *time.Time `json:"suspendedAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// ChannelPage -.
type ChannelPage struct {
	Items     []Channel `json:"items"`
	NextAfter *string   `json:"nextAfter"`
}

// Follow is one follow of GET /internal/v1/follows.
type Follow struct {
	UserID      string    `json:"userId"`
	ChannelID   string    `json:"channelId"`
	NotifyLevel string    `json:"notifyLevel"`
	CreatedAt   time.Time `json:"createdAt"`
}

// FollowPage -.
type FollowPage struct {
	Items     []Follow `json:"items"`
	NextAfter *string  `json:"nextAfter"`
}

type pageQuery struct {
	After string `query:"after"`
	Limit int    `query:"limit"`
}

// NewRouter -.
func NewRouter(app *fiber.App, snap usecase.Snapshot, secret string, l logger.Interface) {
	app.Use(recover.New())
	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	g := app.Group("/internal/v1", func(ctx *fiber.Ctx) error {
		if subtle.ConstantTimeCompare([]byte(ctx.Get(SecretHeader)), []byte(secret)) != 1 {
			return fail(ctx, http.StatusUnauthorized, "UNAUTHORIZED")
		}

		return ctx.Next()
	})

	g.Get("/channels", func(ctx *fiber.Ctx) error {
		var q pageQuery
		if err := ctx.QueryParser(&q); err != nil {
			return fail(ctx, http.StatusBadRequest, "VALIDATION_FAILED")
		}

		items, next, err := snap.Channels(ctx.UserContext(), q.After, q.Limit)
		if err != nil {
			return failErr(ctx, l, "channels", err)
		}

		out := ChannelPage{Items: make([]Channel, 0, len(items)), NextAfter: nextOf(next)}
		for _, c := range items {
			out.Items = append(out.Items, Channel{
				ID: c.ID, OwnerID: c.OwnerID, Handle: c.Handle, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL,
				Status: string(c.Status), SuspendedAt: c.SuspendedAt, UpdatedAt: c.UpdatedAt.UTC(),
			})
		}

		return ctx.JSON(out)
	})

	g.Get("/follows", func(ctx *fiber.Ctx) error {
		var q pageQuery
		if err := ctx.QueryParser(&q); err != nil {
			return fail(ctx, http.StatusBadRequest, "VALIDATION_FAILED")
		}

		items, next, err := snap.Follows(ctx.UserContext(), q.After, q.Limit)
		if err != nil {
			return failErr(ctx, l, "follows", err)
		}

		out := FollowPage{Items: make([]Follow, 0, len(items)), NextAfter: nextOf(next)}
		for _, f := range items {
			out.Items = append(out.Items, Follow{
				UserID: f.UserID, ChannelID: f.ChannelID, NotifyLevel: f.NotifyLevel, CreatedAt: f.CreatedAt.UTC(),
			})
		}

		return ctx.JSON(out)
	})
}

func nextOf(next string) *string {
	if next == "" {
		return nil
	}

	return &next
}

func failErr(ctx *fiber.Ctx, l logger.Interface, op string, err error) error {
	if errors.Is(err, entity.ErrInvalidCursor) {
		return fail(ctx, http.StatusBadRequest, "VALIDATION_FAILED")
	}

	l.Error("internalapi - %s: %v", op, err)

	return fail(ctx, http.StatusInternalServerError, "INTERNAL_ERROR")
}

func fail(ctx *fiber.Ctx, status int, code string) error {
	return ctx.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code}})
}
