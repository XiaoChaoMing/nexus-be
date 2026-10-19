package v1

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// mySubscriptions -.
//
//	@Summary     The caller's subscriptions
//	@Description status=current (default, by period end ascending) or expired (newest first). With channelId and no status: at most one row — the current subscription of that channel, else the most recently ended one (Subscribe button state).
//	@Tags        subscriptions
//	@Produce     json
//	@Security    BearerAuth
//	@Param       status    query    string false "current | expired"
//	@Param       channelId query    string false "channel id"
//	@Param       cursor    query    string false "nextCursor of the previous page"
//	@Param       limit     query    int    false "1–50, default 20"
//	@Success     200       {object} Data[Page[Sub]]
//	@Failure     400       {object} Error "VALIDATION_FAILED"
//	@Failure     401       {object} Error "UNAUTHORIZED"
//	@Router      /me/subscriptions [get]
func (r *routes) mySubscriptions(ctx *fiber.Ctx) error {
	userID, cur, limit, ok, answered := pagedCaller(ctx)
	if !ok {
		return answered
	}

	p, err := r.uc.Subscriptions.MySubscriptions(ctx.UserContext(), userID, usecase.SubQuery{
		Status: ctx.Query("status"), ChannelID: optional(ctx, "channelId"),
	}, cur, limit)
	if err != nil {
		return r.fail(ctx, "mySubscriptions", err)
	}

	return ctx.JSON(Data[Page[Sub]]{Data: Page[Sub]{
		Items: NewSubs(p.Items, time.Now()), NextCursor: p.Next,
	}})
}

// cancelSubscription -.
//
//	@Summary     Stop renewal reminders
//	@Description ACTIVE → CANCELLED_PENDING_END; benefits stay until currentPeriodEnd. Requires Idempotency-Key. A second cancel → 409.
//	@Tags        subscriptions
//	@Produce     json
//	@Security    BearerAuth
//	@Param       Idempotency-Key header   string true "UUID"
//	@Param       id              path     string true "subscription id"
//	@Success     200             {object} Data[Sub]
//	@Failure     400             {object} Error "VALIDATION_FAILED"
//	@Failure     401             {object} Error "UNAUTHORIZED"
//	@Failure     404             {object} Error "NOT_FOUND"
//	@Failure     409             {object} Error "INVALID_STATE / CONFLICT"
//	@Failure     422             {object} Error "IDEMPOTENCY_KEY_REUSED"
//	@Router      /me/subscriptions/{id}/cancel [post]
func (r *routes) cancelSubscription(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	s, err := r.uc.Subscriptions.CancelSubscription(ctx.UserContext(), userID, ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "cancelSubscription", err)
	}

	return ctx.JSON(Data[Sub]{Data: NewSub(s, time.Now())})
}

// resumeSubscription -.
//
//	@Summary     Turn renewal reminders back on
//	@Description CANCELLED_PENDING_END → ACTIVE while the period runs; otherwise 409.
//	@Tags        subscriptions
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "subscription id"
//	@Success     200 {object} Data[Sub]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     404 {object} Error "NOT_FOUND"
//	@Failure     409 {object} Error "INVALID_STATE"
//	@Router      /me/subscriptions/{id}/resume [post]
func (r *routes) resumeSubscription(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	s, err := r.uc.Subscriptions.ResumeSubscription(ctx.UserContext(), userID, ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "resumeSubscription", err)
	}

	return ctx.JSON(Data[Sub]{Data: NewSub(s, time.Now())})
}

// myPayments -.
//
//	@Summary     The caller's payment history
//	@Description Checkout orders, newest first; status is an optional CSV of PENDING, PAID, CANCELLED, FAILED, EXPIRED, VOIDED.
//	@Tags        subscriptions
//	@Produce     json
//	@Security    BearerAuth
//	@Param       status query    string false "CSV of order statuses"
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "1–50, default 20"
//	@Success     200    {object} Data[Page[Order]]
//	@Failure     400    {object} Error "VALIDATION_FAILED"
//	@Failure     401    {object} Error "UNAUTHORIZED"
//	@Router      /me/payments [get]
func (r *routes) myPayments(ctx *fiber.Ctx) error {
	userID, cur, limit, ok, answered := pagedCaller(ctx)
	if !ok {
		return answered
	}

	p, err := r.uc.Subscriptions.MyPayments(ctx.UserContext(), userID, csv(ctx.Query("status")), cur, limit)
	if err != nil {
		return r.fail(ctx, "myPayments", err)
	}

	return ctx.JSON(Data[Page[Order]]{Data: Page[Order]{
		Items: NewOrders(p.Items), NextCursor: p.Next,
	}})
}
