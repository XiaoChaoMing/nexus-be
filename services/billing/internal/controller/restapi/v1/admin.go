package v1

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// adminPayouts -.
//
//	@Summary     Payout queue
//	@Description status REQUESTED (default, oldest first), APPROVED, REJECTED or ALL (newest first); each row carries the channel balance.
//	@Tags        admin-billing
//	@Produce     json
//	@Security    BearerAuth
//	@Param       status query    string false "REQUESTED | APPROVED | REJECTED | ALL"
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
//	@Description REQUESTED → APPROVED with a PAYOUT debit in the ledger (no money is moved). A reviewed payout → 409.
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
//	@Description reason 10–500 characters after trim; the amount is available again. A reviewed payout → 409.
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
	adminID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	var body RejectRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
	}

	p, err := r.uc.Admin.Reject(ctx.UserContext(), adminID, ctx.Params("id"), body.Reason)
	if err != nil {
		return r.fail(ctx, "reject", err)
	}

	return ctx.JSON(Data[AdminPayout]{Data: NewAdminPayout(p)})
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
