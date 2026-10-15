// Package internalapi serves safety-svc on the internal port (never exposed through the edge): the messages
// captured as report evidence.
package internalapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// SecretHeader carries the shared secret between internal services.
const SecretHeader = "X-Internal-Secret" //nolint:gosec // a header name, not a secret

// EvidenceItem is one captured message; a deleted one keeps its original text here (admins only).
type EvidenceItem struct {
	MessageID string            `json:"messageId"`
	UserID    string            `json:"userId"`
	Body      string            `json:"body"`
	Fragments []entity.Fragment `json:"fragments"`
	CreatedAt time.Time         `json:"createdAt"`
	Deleted   bool              `json:"deleted"`
}

// EvidenceList is oldest first.
type EvidenceList struct {
	Items []EvidenceItem `json:"items"`
}

// NewRouter -.
func NewRouter(app *fiber.App, ev usecase.Evidence, secret string, l logger.Interface) {
	app.Use(recover.New())
	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	g := app.Group("/internal/v1", func(ctx *fiber.Ctx) error {
		if subtle.ConstantTimeCompare([]byte(ctx.Get(SecretHeader)), []byte(secret)) != 1 {
			return fail(ctx, http.StatusUnauthorized, entity.CodeUnauthorized)
		}

		return ctx.Next()
	})

	g.Get("/chat/evidence", evidence(ev, l))
}

type evidenceQuery struct {
	streamID  string
	messageID *string
	userID    *string
	limit     int
}

// parseEvidence: streamId and exactly one of messageId / userId, all uuids; limit 1–10.
func parseEvidence(ctx *fiber.Ctx) (evidenceQuery, bool) {
	q := evidenceQuery{streamID: ctx.Query("streamId"), limit: ctx.QueryInt("limit", entity.EvidenceMax)}
	messageID, userID := ctx.Query("messageId"), ctx.Query("userId")

	if uuid.Validate(q.streamID) != nil || (messageID == "") == (userID == "") || q.limit < 1 || q.limit > entity.EvidenceMax {
		return q, false
	}

	id := messageID + userID
	if uuid.Validate(id) != nil {
		return q, false
	}

	if messageID != "" {
		q.messageID = &id
	} else {
		q.userID = &id
	}

	return q, true
}

func evidence(ev usecase.Evidence, l logger.Interface) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		q, ok := parseEvidence(ctx)
		if !ok {
			return fail(ctx, http.StatusBadRequest, entity.CodeValidationFailed)
		}

		items, err := ev.Collect(ctx.UserContext(), q.streamID, q.messageID, q.userID, q.limit)

		switch {
		case errors.Is(err, entity.ErrNotFound):
			return fail(ctx, http.StatusNotFound, entity.CodeNotFound)
		case err != nil:
			l.Error("internalapi - evidence: %v", err)

			return fail(ctx, http.StatusInternalServerError, entity.CodeInternal)
		}

		out := EvidenceList{Items: make([]EvidenceItem, 0, len(items))}
		for _, it := range items {
			out.Items = append(out.Items, EvidenceItem(it))
		}

		return ctx.JSON(out)
	}
}

func fail(ctx *fiber.Ctx, status int, code string) error {
	return ctx.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code}})
}
