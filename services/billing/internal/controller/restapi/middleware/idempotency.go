// Package middleware holds the auth and Idempotency-Key handlers of billing-svc's REST API.
package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// HeaderIdempotencyKey -.
const HeaderIdempotencyKey = "Idempotency-Key"

type errBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

func fail(ctx *fiber.Ctx, status int, code, message string, details map[string]string) error {
	return ctx.Status(status).JSON(fiber.Map{"error": errBody{Code: code, Message: message, Details: details}})
}

// Idempotency implements NXL-00 §8 on money routes: the same key and body answer the stored response, the
// same key with another body is IDEMPOTENCY_KEY_REUSED, a key still running is CONFLICT. Only 2xx answers
// are stored; an error is recomputed on retry. Runs after Auth.
func Idempotency(uc usecase.Idempotency, l logger.Interface) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		userID, ok := UserID(ctx)
		if !ok {
			return unauthorized(ctx)
		}

		key := ctx.Get(HeaderIdempotencyKey)

		switch {
		case key == "":
			return fail(ctx, http.StatusBadRequest, "VALIDATION_FAILED", "validation failed",
				map[string]string{"idempotencyKey": "REQUIRED"})
		case uuid.Validate(key) != nil:
			return fail(ctx, http.StatusBadRequest, "VALIDATION_FAILED", "validation failed",
				map[string]string{"idempotencyKey": "FORMAT"})
		}

		hash, ok := CanonicalHash(ctx.Body())
		if !ok {
			return ctx.Next() // the handler answers 400 for the malformed body; nothing is stored
		}

		k := usecase.IdemKey{UserID: userID, Method: ctx.Method(), Path: ctx.Path(), Key: key}

		st, err := uc.Begin(ctx.UserContext(), k, hash)
		if err != nil {
			l.Error("middleware - idempotency - begin: %v", err)

			return fail(ctx, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error", nil)
		}

		switch st.Outcome {
		case usecase.IdemMismatch:
			return fail(ctx, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED",
				"this Idempotency-Key was used with another request", nil)
		case usecase.IdemInProgress:
			return fail(ctx, http.StatusConflict, "CONFLICT", "a request with this Idempotency-Key is still running", nil)
		case usecase.IdemDone:
			ctx.Set("Idempotent-Replay", "true")
			ctx.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)

			return ctx.Status(st.Status).Send(st.Body)
		case usecase.IdemNew:
		}

		return run(ctx, uc, k, l)
	}
}

func run(ctx *fiber.Ctx, uc usecase.Idempotency, k usecase.IdemKey, l logger.Interface) error {
	herr := ctx.Next()
	status := ctx.Response().StatusCode()

	if herr == nil && status >= http.StatusOK && status < http.StatusMultipleChoices {
		body := bytes.Clone(ctx.Response().Body())
		if err := uc.Complete(ctx.UserContext(), k, status, body); err != nil {
			l.Error("middleware - idempotency - complete: %v", err)
		}

		return nil
	}

	if err := uc.Abort(ctx.UserContext(), k); err != nil {
		l.Error("middleware - idempotency - abort: %v", err)
	}

	return herr
}

// CanonicalHash is SHA-256 of the body re-encoded with sorted keys and no spaces; empty body = SHA-256("").
// ok is false for a body that is not JSON.
func CanonicalHash(body []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		sum := sha256.Sum256(nil)

		return sum[:], true
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return nil, false
	}

	canon, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}

	sum := sha256.Sum256(canon)

	return sum[:], true
}
