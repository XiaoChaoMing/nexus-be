// Package v1 is the public REST API of billing-svc.
package v1

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// Error codes shared with the web and admin clients.
const (
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeNotFound         = "NOT_FOUND"
	CodeForbidden        = "FORBIDDEN"
	CodeNotCreator       = "NOT_CREATOR"
	CodeChannelSuspended = "CHANNEL_SUSPENDED"
	CodeSubscribeSelf    = "CANNOT_SUBSCRIBE_SELF"
	CodeGiftSelf         = "CANNOT_GIFT_SELF"
	CodeInvalidState     = "INVALID_STATE"
	CodeConflict         = "CONFLICT"
	CodeRateLimited      = "RATE_LIMITED"
	CodeInternal         = "INTERNAL_ERROR"

	_fieldBody    = "body"
	_defaultLimit = 20
	_maxLimit     = 50
)

// UseCases are the business contracts the routes call.
type UseCases struct {
	Catalog       usecase.Catalog
	Checkout      usecase.Checkout
	Subscriptions usecase.Subscriptions
	Studio        usecase.Studio
	Admin         usecase.Admin
	Idempotency   usecase.Idempotency
	Wallet        usecase.Wallet
	Gifts         usecase.Gifts
}

type routes struct {
	uc UseCases
	l  logger.Interface
}

// NewRoutes registers the billing routes on /v1; auth verifies the token, admin requires role ADMIN.
func NewRoutes(g fiber.Router, uc UseCases, auth, admin fiber.Handler, l logger.Interface) {
	r := &routes{uc: uc, l: l}
	idem := middleware.Idempotency(uc.Idempotency, l)

	g.Get("/channels/:channelId/subscription-tiers", r.publicTiers)
	g.Get("/checkout/config", r.config)
	g.Get("/checkout/tiers/:tierId", r.tierContext)
	g.Post("/checkout/orders", auth, idem, r.createOrder)
	g.Get("/checkout/orders/:id", auth, r.getOrder)
	g.Post("/checkout/orders/:id/sync", auth, r.syncOrder)
	g.Post("/checkout/orders/:id/cancel", auth, r.cancelOrder)

	g.Get("/me/subscriptions", auth, r.mySubscriptions)
	g.Post("/me/subscriptions/:id/cancel", auth, idem, r.cancelSubscription)
	g.Post("/me/subscriptions/:id/resume", auth, r.resumeSubscription)
	g.Get("/me/payments", auth, r.myPayments)
	g.Get("/me/wallet", auth, r.wallet)
	g.Get("/me/wallet/entries", auth, r.walletEntries)
	g.Post("/checkout/topups", auth, idem, r.createTopup)
	g.Get("/gift-items", r.giftItems)
	g.Post("/channels/:channelId/gifts", auth, idem, r.sendGift)

	s := g.Group("/studio/monetization", auth)
	s.Get("/tiers", r.studioTiers)
	s.Patch("/tiers/:level", r.updateTier)
	s.Get("/summary", r.summary)
	s.Get("/ledger", r.ledger)
	s.Get("/payouts", r.studioPayouts)
	s.Post("/payouts", idem, r.requestPayout)

	a := g.Group("/admin", auth, admin)
	a.Get("/payouts", r.adminPayouts)
	a.Get("/payouts/pending-count", r.pendingCount)
	a.Get("/payouts/:id", r.adminPayout)
	a.Get("/payouts/:id/bank-account", r.bankAccount)
	a.Post("/payouts/:id/approve", r.approve)
	a.Post("/payouts/:id/reject", r.reject)
	a.Post("/payouts/:id/mark-paid", r.markPaid)
	a.Post("/payouts/:id/mark-failed", r.markFailed)
	a.Get("/checkout-orders", r.adminOrders)
	a.Get("/checkout-orders/:id", r.adminOrder)
	a.Get("/gift-items", r.adminGiftItems)
	a.Post("/gift-items", r.createGiftItem)
	a.Patch("/gift-items/:id", r.updateGiftItem)
}

func errorJSON(ctx *fiber.Ctx, status int, code, message string, details map[string]string) error {
	return ctx.Status(status).JSON(Error{Error: ErrorBody{Code: code, Message: message, Details: details}})
}

func validationFailed(ctx *fiber.Ctx, details map[string]string) error {
	return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "validation failed", details)
}

// fail maps a use-case error to the NXL-00 §4 envelope; anything unmapped is logged and answered 500.
func (r *routes) fail(ctx *fiber.Ctx, op string, err error) error {
	var rl *entity.RateLimitError

	switch {
	case errors.Is(err, entity.ErrValidation):
		return validationFailed(ctx, entity.DetailsOf(err))
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, entity.ErrNotCreator):
		return errorJSON(ctx, http.StatusForbidden, CodeNotCreator, "you have no channel", nil)
	case errors.Is(err, entity.ErrChannelSuspended):
		return errorJSON(ctx, http.StatusForbidden, CodeChannelSuspended, "the channel is suspended", nil)
	case errors.Is(err, entity.ErrSubscribeSelf):
		return errorJSON(ctx, http.StatusUnprocessableEntity, CodeSubscribeSelf, "you cannot subscribe to your own channel", nil)
	case errors.Is(err, entity.ErrGiftSelf):
		return errorJSON(ctx, http.StatusUnprocessableEntity, CodeGiftSelf, "you cannot give a gift to your own channel", nil)
	case errors.Is(err, entity.ErrInvalidState):
		return errorJSON(ctx, http.StatusConflict, CodeInvalidState, "invalid state", entity.DetailsOf(err))
	case errors.Is(err, entity.ErrPendingLimit):
		return errorJSON(ctx, http.StatusConflict, CodeConflict, "too many pending payments",
			map[string]string{"limit": strconv.Itoa(entity.MaxPendingOrders)})
	case errors.As(err, &rl):
		sec := strconv.Itoa(int(math.Ceil(rl.RetryAfter.Seconds())))
		ctx.Set(fiber.HeaderRetryAfter, sec)

		return errorJSON(ctx, http.StatusTooManyRequests, CodeRateLimited, "too many requests",
			map[string]string{"retryAfterSec": sec})
	}

	r.l.Error("restapi - v1 - %s: %v", op, err)

	return errorJSON(ctx, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
}

func caller(ctx *fiber.Ctx) (string, bool) { return middleware.UserID(ctx) }

func unauthorized(ctx *fiber.Ctx) error {
	return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
}

// pageQuery reads cursor and limit (1–50, default 20).
func pageQuery(ctx *fiber.Ctx) (cur string, limit int, ok bool) {
	limit = _defaultLimit

	if s := ctx.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > _maxLimit {
			return "", 0, false
		}

		limit = n
	}

	return ctx.Query("cursor"), limit, true
}

// pagedCaller reads the caller and the page query; when ok is false the error answer is written and the
// handler returns answered.
func pagedCaller(ctx *fiber.Ctx) (userID, cur string, limit int, ok bool, answered error) {
	if userID, ok = caller(ctx); !ok {
		return "", "", 0, false, unauthorized(ctx)
	}

	if cur, limit, ok = pageQuery(ctx); !ok {
		return "", "", 0, false, badLimit(ctx)
	}

	return userID, cur, limit, true, nil
}

func badLimit(ctx *fiber.Ctx) error {
	return validationFailed(ctx, map[string]string{"limit": entity.DetailFormat})
}

// csv splits a comma-separated query value.
func csv(s string) []string {
	var out []string

	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

func optional(ctx *fiber.Ctx, key string) *string {
	if v := ctx.Query(key); v != "" {
		return &v
	}

	return nil
}
