// Package internalapi serves other services on the internal port (never exposed through the edge): the
// account and block snapshots safety-svc and notification-svc backfill their copies from.
package internalapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

// SecretHeader carries the shared secret between internal services.
const SecretHeader = "X-Internal-Secret" //nolint:gosec // a header name, not a secret

// User is one account of GET /internal/v1/users.
type User struct {
	ID            string     `json:"id"`
	Username      string     `json:"username"`
	DisplayName   string     `json:"displayName"`
	Email         string     `json:"email"`
	EmailVerified bool       `json:"emailVerified"`
	Locale        string     `json:"locale"`
	Timezone      string     `json:"timezone"`
	GlobalRole    string     `json:"globalRole"`
	Status        string     `json:"status"`
	LockedUntil   *time.Time `json:"lockedUntil"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// UserPage -.
type UserPage struct {
	Items     []User  `json:"items"`
	NextAfter *string `json:"nextAfter"`
}

// BlockRow is one row of GET /internal/v1/user-blocks.
type BlockRow struct {
	BlockerID string `json:"blockerId"`
	BlockedID string `json:"blockedId"`
}

// BlockPage -.
type BlockPage struct {
	Items     []BlockRow `json:"items"`
	NextAfter *string    `json:"nextAfter"`
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

	g.Get("/users", func(ctx *fiber.Ctx) error {
		var q pageQuery
		if err := ctx.QueryParser(&q); err != nil {
			return fail(ctx, http.StatusBadRequest, "VALIDATION_FAILED")
		}

		users, next, err := snap.Users(ctx.UserContext(), q.After, q.Limit)
		if err != nil {
			return failErr(ctx, l, "users", err)
		}

		out := UserPage{Items: make([]User, 0, len(users)), NextAfter: nextOf(next)}
		for _, u := range users {
			out.Items = append(out.Items, User{
				ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email, EmailVerified: u.IsEmailVerified(),
				Locale: u.Locale, Timezone: u.Timezone, GlobalRole: u.GlobalRole, Status: string(u.Status), LockedUntil: u.LockedUntil,
				CreatedAt: u.CreatedAt.UTC(), UpdatedAt: u.UpdatedAt.UTC(),
			})
		}

		return ctx.JSON(out)
	})

	g.Get("/user-blocks", func(ctx *fiber.Ctx) error {
		var q pageQuery
		if err := ctx.QueryParser(&q); err != nil {
			return fail(ctx, http.StatusBadRequest, "VALIDATION_FAILED")
		}

		rows, next, err := snap.Blocks(ctx.UserContext(), q.After, q.Limit)
		if err != nil {
			return failErr(ctx, l, "user-blocks", err)
		}

		out := BlockPage{Items: make([]BlockRow, 0, len(rows)), NextAfter: nextOf(next)}
		for _, b := range rows {
			out.Items = append(out.Items, BlockRow(b))
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
