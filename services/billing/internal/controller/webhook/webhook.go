// Package webhook receives the payment provider's IPN (POST /v1/webhooks/sepay). It answers plain HTTP
// statuses with {"success": bool}, not the NXL-00 envelope: the caller is SePay, not a NexusLive client.
package webhook

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// Path is the IPN URL configured on the provider dashboard.
const Path = "/v1/webhooks/sepay"

type result struct {
	Success bool `json:"success"`
}

// Register mounts the webhook; no JWT, no CORS. Headers and bodies are never logged (secret, payer data).
func Register(app fiber.Router, uc usecase.Payments, l logger.Interface) {
	app.Post(Path, Handler(uc, l))
}

// Handler -.
//
//	@Summary     SePay IPN
//	@Description Called by SePay with X-Secret-Key. 200 {"success":true} when applied, a duplicate, ignored, not approved, an unknown invoice or an amount mismatch (the order becomes FAILED AMOUNT_MISMATCH); 401 wrong secret; 400 malformed or over 64 KB; 500 temporary error (SePay retries).
//	@Tags        webhooks
//	@Accept      json
//	@Produce     json
//	@Param       X-Secret-Key header string true "IPN secret"
//	@Success     200
//	@Failure     400
//	@Failure     401
//	@Failure     500
//	@Router      /webhooks/sepay [post]
func Handler(uc usecase.Payments, l logger.Interface) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		body := ctx.Body()
		if len(body) > entity.WebhookBodyMax {
			return ctx.Status(http.StatusBadRequest).JSON(result{})
		}

		header := http.Header{}

		ctx.Request().Header.VisitAll(func(k, v []byte) { header.Add(string(k), string(v)) })

		res, err := uc.Notify(ctx.UserContext(), header, body)

		switch {
		case errors.Is(err, entity.ErrUnauthorized):
			l.Warn("webhook - sepay: secret mismatch from %s", ctx.IP())

			return ctx.Status(http.StatusUnauthorized).JSON(result{})
		case errors.Is(err, entity.ErrMalformed):
			l.Warn("webhook - sepay: malformed notification (%d bytes): %v", len(body), err)

			return ctx.Status(http.StatusBadRequest).JSON(result{})
		case err != nil:
			l.Error("webhook - sepay: %v", err)

			return ctx.Status(http.StatusInternalServerError).JSON(result{})
		}

		l.Info("webhook - sepay: %s", res)

		return ctx.JSON(result{Success: true})
	}
}
