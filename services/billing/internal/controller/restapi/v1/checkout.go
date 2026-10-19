package v1

import (
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// publicTiers -.
//
//	@Summary     Subscription tiers of a channel
//	@Description Active tiers by level (AC-H01). Unknown channel → 404, suspended channel → 403.
//	@Tags        tiers
//	@Produce     json
//	@Param       channelId path     string true "channel id"
//	@Success     200       {object} Data[Page[Tier]]
//	@Failure     403       {object} Error "CHANNEL_SUSPENDED"
//	@Failure     404       {object} Error "NOT_FOUND"
//	@Router      /channels/{channelId}/subscription-tiers [get]
func (r *routes) publicTiers(ctx *fiber.Ctx) error {
	tiers, err := r.uc.Catalog.PublicTiers(ctx.UserContext(), ctx.Params("channelId"))
	if err != nil {
		return r.fail(ctx, "publicTiers", err)
	}

	return ctx.JSON(Data[Page[Tier]]{Data: Page[Tier]{Items: NewTiers(tiers)}})
}

// config -.
//
//	@Summary     Checkout settings
//	@Description Payment methods offered on the modal (BILLING_SEPAY_METHODS) and the fixed rules.
//	@Tags        checkout
//	@Produce     json
//	@Success     200 {object} Data[CheckoutConfig]
//	@Router      /checkout/config [get]
func (r *routes) config(ctx *fiber.Ctx) error {
	return ctx.JSON(Data[CheckoutConfig]{Data: NewCheckoutConfig(r.uc.Catalog.Config())})
}

// tierContext -.
//
//	@Summary     A tier with its channel (for /checkout?tierId=)
//	@Description The channel and its active tiers; the requested tier is included even when inactive.
//	@Tags        checkout
//	@Produce     json
//	@Param       tierId path     string true "tier id"
//	@Success     200    {object} Data[TierContext]
//	@Failure     403    {object} Error "CHANNEL_SUSPENDED"
//	@Failure     404    {object} Error "NOT_FOUND"
//	@Router      /checkout/tiers/{tierId} [get]
func (r *routes) tierContext(ctx *fiber.Ctx) error {
	id := ctx.Params("tierId")

	ch, tiers, err := r.uc.Catalog.TierContext(ctx.UserContext(), id)
	if err != nil {
		return r.fail(ctx, "tierContext", err)
	}

	return ctx.JSON(Data[TierContext]{Data: NewTierContext(id, ch, tiers)})
}

// createOrder -.
//
//	@Summary     Start a subscription checkout
//	@Description Creates a PENDING order (30 min) and the signed form to post to the payment page, fields in order. The caller's pending order of the same tier is returned with 200 (re-signed). Requires Idempotency-Key (UUID): same key and body → the stored answer with Idempotent-Replay: true; same key, other body → 422 IDEMPOTENCY_KEY_REUSED; still running → 409 CONFLICT. 409 INVALID_STATE details.reason: TIER_INACTIVE, TIER_CHANGE (currentTier, currentPeriodEnd), MAX_PREPAID (currentPeriodEnd, maxPeriodEnd), ORDER_PENDING (orderId, tierLevel). 409 CONFLICT details.limit = 5 pending orders.
//	@Tags        checkout
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       Idempotency-Key header   string              true "UUID"
//	@Param       body            body     CreateOrderRequest true "order"
//	@Success     201             {object} Data[OrderCheckout]
//	@Success     200             {object} Data[OrderCheckout] "pending order of the same tier"
//	@Failure     400             {object} Error "VALIDATION_FAILED"
//	@Failure     401             {object} Error "UNAUTHORIZED"
//	@Failure     403             {object} Error "CHANNEL_SUSPENDED"
//	@Failure     404             {object} Error "NOT_FOUND"
//	@Failure     409             {object} Error "INVALID_STATE / CONFLICT"
//	@Failure     422             {object} Error "CANNOT_SUBSCRIBE_SELF / IDEMPOTENCY_KEY_REUSED"
//	@Router      /checkout/orders [post]
func (r *routes) createOrder(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	var body CreateOrderRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
	}

	o, c, created, err := r.uc.Checkout.CreateOrder(ctx.UserContext(), userID, usecase.CreateOrderInput{
		TierID: body.TierID, PaymentMethod: body.PaymentMethod, IdempotencyKey: ctx.Get(middleware.HeaderIdempotencyKey),
	})
	if err != nil {
		return r.fail(ctx, "createOrder", err)
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	return ctx.Status(status).JSON(Data[OrderCheckout]{Data: OrderCheckout{
		Order: NewOrder(o), Checkout: NewCheckout(c),
	}})
}

// getOrder -.
//
//	@Summary     A checkout order of the caller
//	@Description With the subscription it created; an order in review shows the caller's current subscription of the channel. Another user's order → 404.
//	@Tags        checkout
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "order id"
//	@Success     200 {object} Data[OrderView]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     404 {object} Error "NOT_FOUND"
//	@Router      /checkout/orders/{id} [get]
func (r *routes) getOrder(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	v, err := r.uc.Checkout.GetOrder(ctx.UserContext(), userID, ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "getOrder", err)
	}

	return ctx.JSON(Data[OrderView]{Data: NewOrderView(v, time.Now())})
}

// syncOrder -.
//
//	@Summary     Check a pending order with the payment provider
//	@Description Called by /checkout/result. Paid at the provider → PAID; outcome CANCEL → CANCELLED; ERROR → FAILED (reason from the lookup); SUCCESS and not paid yet → stays PENDING (poll GET). The redirect alone never marks an order paid. At most 10 calls a minute per order (429 details.retryAfterSec).
//	@Tags        checkout
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string       true "order id"
//	@Param       body body     SyncRequest true "redirect outcome"
//	@Success     200  {object} Data[OrderView]
//	@Failure     400  {object} Error "VALIDATION_FAILED"
//	@Failure     401  {object} Error "UNAUTHORIZED"
//	@Failure     404  {object} Error "NOT_FOUND"
//	@Failure     429  {object} Error "RATE_LIMITED"
//	@Router      /checkout/orders/{id}/sync [post]
func (r *routes) syncOrder(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	var body SyncRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
	}

	switch usecase.Outcome(body.Outcome) {
	case usecase.OutcomeSuccess, usecase.OutcomeError, usecase.OutcomeCancel:
	default:
		code := entity.DetailEnum
		if body.Outcome == "" {
			code = entity.DetailRequired
		}

		return validationFailed(ctx, map[string]string{"outcome": code})
	}

	v, err := r.uc.Checkout.SyncOrder(ctx.UserContext(), userID, ctx.Params("id"), usecase.Outcome(body.Outcome))
	if err != nil {
		return r.fail(ctx, "syncOrder", err)
	}

	return ctx.JSON(Data[OrderView]{Data: NewOrderView(v, time.Now())})
}

// cancelOrder -.
//
//	@Summary     Cancel a pending order
//	@Description Only PENDING orders (else 409). The payment provider is told best effort; money that still arrives wins (the order becomes PAID).
//	@Tags        checkout
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "order id"
//	@Success     200 {object} Data[OrderOnly]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     404 {object} Error "NOT_FOUND"
//	@Failure     409 {object} Error "INVALID_STATE"
//	@Router      /checkout/orders/{id}/cancel [post]
func (r *routes) cancelOrder(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	o, err := r.uc.Checkout.CancelOrder(ctx.UserContext(), userID, ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "cancelOrder", err)
	}

	return ctx.JSON(Data[OrderOnly]{Data: OrderOnly{Order: NewOrder(o)}})
}
