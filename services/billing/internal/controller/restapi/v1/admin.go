package v1

import (
	"context"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// adminPayouts -.
//
//	@Summary     Payout queue
//	@Description status REQUESTED (default, oldest first), APPROVED, PROCESSING, PAID, REJECTED, FAILED or ALL (newest first); each row carries the channel balance (balance = available + held by open payouts).
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Param       status query    string false "REQUESTED | APPROVED | PROCESSING | PAID | REJECTED | FAILED | ALL"
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "1–50, default 20"
//	@Success     200    {object} Data[Page[AdminPayout]]
//	@Failure     400    {object} Error "VALIDATION_FAILED"
//	@Failure     401    {object} Error "UNAUTHORIZED"
//	@Failure     403    {object} Error "FORBIDDEN"
//	@Router      /admin/payouts [get]
func (r *routes) adminPayouts(ctx *fiber.Ctx) error {
	cur, limit, ok := pageQuery(ctx)
	if !ok {
		return badLimit(ctx)
	}

	p, err := r.uc.Admin.AdminPayouts(ctx.UserContext(), ctx.Query("status"), cur, limit)
	if err != nil {
		return r.fail(ctx, "adminPayouts", err)
	}

	return ctx.JSON(Data[Page[AdminPayout]]{Data: Page[AdminPayout]{
		Items: NewAdminPayouts(p.Items), NextCursor: p.Next,
	}})
}

// pendingCount -.
//
//	@Summary     Requested payouts (admin menu badge)
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} Data[PendingCount]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     403 {object} Error "FORBIDDEN"
//	@Router      /admin/payouts/pending-count [get]
func (r *routes) pendingCount(ctx *fiber.Ctx) error {
	n, err := r.uc.Admin.PendingCount(ctx.UserContext())
	if err != nil {
		return r.fail(ctx, "pendingCount", err)
	}

	return ctx.JSON(Data[PendingCount]{Data: PendingCount{Requested: n}})
}

// adminPayout -.
//
//	@Summary     A payout request
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "payout id"
//	@Success     200 {object} Data[AdminPayout]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     403 {object} Error "FORBIDDEN"
//	@Failure     404 {object} Error "NOT_FOUND"
//	@Router      /admin/payouts/{id} [get]
func (r *routes) adminPayout(ctx *fiber.Ctx) error {
	p, err := r.uc.Admin.AdminPayout(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "adminPayout", err)
	}

	return ctx.JSON(Data[AdminPayout]{Data: NewAdminPayout(p)})
}

// bankAccount -.
//
//	@Summary     The full bank account of a payout
//	@Description Decrypted on demand; every view is written to the audit log (PAYOUT_BANK_INFO_VIEWED). Not cached.
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "payout id"
//	@Success     200 {object} Data[BankAccount]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     403 {object} Error "FORBIDDEN"
//	@Failure     404 {object} Error "NOT_FOUND"
//	@Failure     500 {object} Error "INTERNAL_ERROR (cannot decrypt)"
//	@Router      /admin/payouts/{id}/bank-account [get]
func (r *routes) bankAccount(ctx *fiber.Ctx) error {
	adminID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	a, err := r.uc.Admin.BankAccount(ctx.UserContext(), adminID, ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "bankAccount", err)
	}

	return ctx.JSON(Data[BankAccount]{Data: NewBankAccount(a)})
}

// approve -.
//
//	@Summary     Approve a payout
//	@Description REQUESTED → APPROVED → handed to the payout provider: PROCESSING for the manual provider (the money was held at request time, no ledger entry). An APPROVED payout (failed provider call) is handed over again. Any other status → 409 INVALID_STATE reason PAYOUT_STATE.
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "payout id"
//	@Success     200 {object} Data[AdminPayout]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     403 {object} Error "FORBIDDEN"
//	@Failure     404 {object} Error "NOT_FOUND"
//	@Failure     409 {object} Error "INVALID_STATE"
//	@Router      /admin/payouts/{id}/approve [post]
func (r *routes) approve(ctx *fiber.Ctx) error {
	adminID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	p, err := r.uc.Admin.Approve(ctx.UserContext(), adminID, ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "approve", err)
	}

	return ctx.JSON(Data[AdminPayout]{Data: NewAdminPayout(p)})
}

// reject -.
//
//	@Summary     Reject a payout
//	@Description REQUESTED → REJECTED; reason 10–500 characters after trim; the hold is given back (CREDIT PAYOUT_REVERSAL). Any other status → 409 INVALID_STATE reason PAYOUT_STATE.
//	@Tags        admin-billing
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string         true "payout id"
//	@Param       body body     RejectRequest true "reason"
//	@Success     200  {object} Data[AdminPayout]
//	@Failure     400  {object} Error "VALIDATION_FAILED"
//	@Failure     401  {object} Error "UNAUTHORIZED"
//	@Failure     403  {object} Error "FORBIDDEN"
//	@Failure     404  {object} Error "NOT_FOUND"
//	@Failure     409  {object} Error "INVALID_STATE"
//	@Router      /admin/payouts/{id}/reject [post]
func (r *routes) reject(ctx *fiber.Ctx) error {
	return payoutAction(r, ctx, "reject", func(b RejectRequest) string { return b.Reason }, r.uc.Admin.Reject)
}

// markPaid -.
//
//	@Summary     Mark a payout paid
//	@Description PROCESSING → PAID with the bank transfer reference (4–64 of A–Z a–z 0–9 . _ / -, trimmed). No ledger entry: the held money has left. Any other status → 409 INVALID_STATE reason PAYOUT_STATE.
//	@Tags        admin-billing
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string          true "payout id"
//	@Param       body body     MarkPaidRequest true "reference"
//	@Success     200  {object} Data[AdminPayout]
//	@Failure     400  {object} Error "VALIDATION_FAILED"
//	@Failure     401  {object} Error "UNAUTHORIZED"
//	@Failure     403  {object} Error "FORBIDDEN"
//	@Failure     404  {object} Error "NOT_FOUND"
//	@Failure     409  {object} Error "INVALID_STATE"
//	@Router      /admin/payouts/{id}/mark-paid [post]
func (r *routes) markPaid(ctx *fiber.Ctx) error {
	return payoutAction(r, ctx, "markPaid", func(b MarkPaidRequest) string { return b.Reference }, r.uc.Admin.MarkPaid)
}

// markFailed -.
//
//	@Summary     Mark a payout failed
//	@Description APPROVED or PROCESSING → FAILED; reason 10–500 characters after trim; the hold is given back (CREDIT PAYOUT_REVERSAL). Any other status → 409 INVALID_STATE reason PAYOUT_STATE.
//	@Tags        admin-billing
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string        true "payout id"
//	@Param       body body     RejectRequest true "reason"
//	@Success     200  {object} Data[AdminPayout]
//	@Failure     400  {object} Error "VALIDATION_FAILED"
//	@Failure     401  {object} Error "UNAUTHORIZED"
//	@Failure     403  {object} Error "FORBIDDEN"
//	@Failure     404  {object} Error "NOT_FOUND"
//	@Failure     409  {object} Error "INVALID_STATE"
//	@Router      /admin/payouts/{id}/mark-failed [post]
func (r *routes) markFailed(ctx *fiber.Ctx) error {
	return payoutAction(r, ctx, "markFailed", func(b RejectRequest) string { return b.Reason }, r.uc.Admin.MarkFailed)
}

// adminOrders -.
//
//	@Summary     Checkout orders (review page)
//	@Description Newest first. needsReview=true lists paid orders that could not be applied (another tier was live); status and failureReason are CSV (AND); q (case-insensitive) is a whole invoice number (exact match) or a prefix of one.
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Param       needsReview query    bool   false "true | false"
//	@Param       status      query    string false "CSV of order statuses"
//	@Param       failureReason query  string false "CSV of CANCELLED_BY_USER | DECLINED | EXPIRED | AMOUNT_MISMATCH | PROVIDER_ERROR | UNKNOWN"
//	@Param       q           query    string false "invoice number, exact (NXL + 10 characters) or prefix"
//	@Param       cursor      query    string false "nextCursor of the previous page"
//	@Param       limit       query    int    false "1–50, default 20"
//	@Success     200         {object} Data[Page[AdminOrder]]
//	@Failure     400         {object} Error "VALIDATION_FAILED"
//	@Failure     401         {object} Error "UNAUTHORIZED"
//	@Failure     403         {object} Error "FORBIDDEN"
//	@Router      /admin/checkout-orders [get]
func (r *routes) adminOrders(ctx *fiber.Ctx) error {
	cur, limit, ok := pageQuery(ctx)
	if !ok {
		return badLimit(ctx)
	}

	q := usecase.AdminOrderQuery{
		Statuses: csv(ctx.Query("status")), FailureReasons: csv(ctx.Query("failureReason")), Invoice: ctx.Query("q"),
	}

	if s := ctx.Query("needsReview"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return validationFailed(ctx, map[string]string{"needsReview": entity.DetailFormat})
		}

		q.NeedsReview = &b
	}

	p, err := r.uc.Admin.AdminOrders(ctx.UserContext(), q, cur, limit)
	if err != nil {
		return r.fail(ctx, "adminOrders", err)
	}

	return ctx.JSON(Data[Page[AdminOrder]]{Data: Page[AdminOrder]{
		Items: NewAdminOrders(p.Items), NextCursor: p.Next,
	}})
}

// adminOrder -.
//
//	@Summary     A checkout order (review panel)
//	@Description id is the order id or its invoice number (case-insensitive). Any order, whatever its status.
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "order id or invoice number"
//	@Success     200 {object} Data[AdminOrder]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     403 {object} Error "FORBIDDEN"
//	@Failure     404 {object} Error "NOT_FOUND"
//	@Router      /admin/checkout-orders/{id} [get]
func (r *routes) adminOrder(ctx *fiber.Ctx) error {
	o, err := r.uc.Admin.AdminOrder(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "adminOrder", err)
	}

	return ctx.JSON(Data[AdminOrder]{Data: NewAdminOrder(o)})
}

// payoutAction runs an admin payout action whose JSON body carries one text field (reason or reference).
func payoutAction[B any](r *routes, ctx *fiber.Ctx, op string, field func(B) string,
	act func(ctx context.Context, adminID, id, text string) (usecase.AdminPayout, error),
) error {
	adminID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	var body B
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
	}

	p, err := act(ctx.UserContext(), adminID, ctx.Params("id"), field(body))
	if err != nil {
		return r.fail(ctx, op, err)
	}

	return ctx.JSON(Data[AdminPayout]{Data: NewAdminPayout(p)})
}
