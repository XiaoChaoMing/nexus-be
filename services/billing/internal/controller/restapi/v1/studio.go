package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

const _defaultTZ = "Asia/Ho_Chi_Minh"

// studioTiers -.
//
//	@Summary     The Creator's tiers
//	@Description Every tier of the caller's channel, inactive ones included, with live subscriber counts. No channel → 403 NOT_CREATOR.
//	@Tags        studio-monetization
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} Data[Page[StudioTier]]
//	@Failure     401 {object} Error "UNAUTHORIZED"
//	@Failure     403 {object} Error "NOT_CREATOR"
//	@Router      /studio/monetization/tiers [get]
func (r *routes) studioTiers(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	tiers, err := r.uc.Studio.StudioTiers(ctx.UserContext(), userID)
	if err != nil {
		return r.fail(ctx, "studioTiers", err)
	}

	items := make([]StudioTier, 0, len(tiers))
	for _, t := range tiers {
		items = append(items, NewStudioTier(t))
	}

	return ctx.JSON(Data[Page[StudioTier]]{Data: Page[StudioTier]{Items: items}})
}

// updateTier -.
//
//	@Summary     Turn a tier on / off or edit its description
//	@Description Prices are fixed. Tier 1 cannot be turned off (active = INVALID); description ≤ 200 characters after trim; an empty body → body = REQUIRED.
//	@Tags        studio-monetization
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       level path     int               true "1–3"
//	@Param       body  body     TierPatchRequest true "changes"
//	@Success     200   {object} Data[StudioTier]
//	@Failure     400   {object} Error "VALIDATION_FAILED"
//	@Failure     401   {object} Error "UNAUTHORIZED"
//	@Failure     403   {object} Error "NOT_CREATOR / CHANNEL_SUSPENDED"
//	@Router      /studio/monetization/tiers/{level} [patch]
func (r *routes) updateTier(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	level, err := strconv.Atoi(ctx.Params("level"))
	if err != nil {
		return validationFailed(ctx, map[string]string{"level": entity.DetailEnum})
	}

	var body TierPatchRequest
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(&body); err != nil {
			return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
		}
	}

	t, err := r.uc.Studio.UpdateTier(ctx.UserContext(), userID, level, usecase.TierPatch{
		Active: body.Active, Description: body.Description,
	})
	if err != nil {
		return r.fail(ctx, "updateTier", err)
	}

	return ctx.JSON(Data[StudioTier]{Data: NewStudioTier(t)})
}

// summary -.
//
//	@Summary     Monetization overview
//	@Description Gross, platform fee (5%, rounded down) and net over the range (days start at 00:00 in tz), today's net for the dashboard tile, the ledger balance, pending payouts and live subscribers.
//	@Tags        studio-monetization
//	@Produce     json
//	@Security    BearerAuth
//	@Param       range query    string false "7d (default) | 28d"
//	@Param       tz    query    string false "IANA time zone, default Asia/Ho_Chi_Minh"
//	@Success     200   {object} Data[Summary]
//	@Failure     400   {object} Error "VALIDATION_FAILED"
//	@Failure     401   {object} Error "UNAUTHORIZED"
//	@Failure     403   {object} Error "NOT_CREATOR"
//	@Router      /studio/monetization/summary [get]
func (r *routes) summary(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	tz := ctx.Query("tz", _defaultTZ)

	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" || tz == "Local" {
		return validationFailed(ctx, map[string]string{"tz": entity.DetailFormat})
	}

	s, err := r.uc.Studio.Summary(ctx.UserContext(), userID, ctx.Query("range"), loc)
	if err != nil {
		return r.fail(ctx, "summary", err)
	}

	return ctx.JSON(Data[Summary]{Data: NewSummary(s)})
}

// ledger -.
//
//	@Summary     The channel ledger
//	@Description Append-only entries, newest first: SUB_REVENUE / PLATFORM_FEE of each payment (reversed by a void) and PAYOUT debits.
//	@Tags        studio-monetization
//	@Produce     json
//	@Security    BearerAuth
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "1–50, default 20"
//	@Success     200    {object} Data[Page[LedgerEntry]]
//	@Failure     400    {object} Error "VALIDATION_FAILED"
//	@Failure     401    {object} Error "UNAUTHORIZED"
//	@Failure     403    {object} Error "NOT_CREATOR"
//	@Router      /studio/monetization/ledger [get]
func (r *routes) ledger(ctx *fiber.Ctx) error {
	userID, cur, limit, ok, answered := pagedCaller(ctx)
	if !ok {
		return answered
	}

	p, err := r.uc.Studio.Ledger(ctx.UserContext(), userID, cur, limit)
	if err != nil {
		return r.fail(ctx, "ledger", err)
	}

	return ctx.JSON(Data[Page[LedgerEntry]]{Data: Page[LedgerEntry]{
		Items: NewLedger(p.Items), NextCursor: p.Next,
	}})
}

// studioPayouts -.
//
//	@Summary     The channel's payout requests
//	@Tags        studio-monetization
//	@Produce     json
//	@Security    BearerAuth
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "1–50, default 20"
//	@Success     200    {object} Data[Page[Payout]]
//	@Failure     400    {object} Error "VALIDATION_FAILED"
//	@Failure     401    {object} Error "UNAUTHORIZED"
//	@Failure     403    {object} Error "NOT_CREATOR"
//	@Router      /studio/monetization/payouts [get]
func (r *routes) studioPayouts(ctx *fiber.Ctx) error {
	userID, cur, limit, ok, answered := pagedCaller(ctx)
	if !ok {
		return answered
	}

	p, err := r.uc.Studio.StudioPayouts(ctx.UserContext(), userID, cur, limit)
	if err != nil {
		return r.fail(ctx, "studioPayouts", err)
	}

	return ctx.JSON(Data[Page[Payout]]{Data: Page[Payout]{
		Items: NewPayouts(p.Items), NextCursor: p.Next,
	}})
}

// requestPayout -.
//
//	@Summary     Request a payout
//	@Description amount: integer 100000–1000000000 (REQUIRED / MIN / MAX); bankCode one of VCB, TCB, BIDV, VTB, ACB, MB, VPB, TPB, STB, VIB, SHB, HDB (ENUM); accountNumber 6–20 digits (FORMAT); accountName is trimmed, spaces collapsed, upper-cased, 2–100 chars (LENGTH), A–Z and spaces only (FORMAT). 409 INVALID_STATE details.reason: INSUFFICIENT_BALANCE (+ available) or PAYOUT_PENDING. Requires Idempotency-Key.
//	@Tags        studio-monetization
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       Idempotency-Key header   string         true "UUID"
//	@Param       body            body     PayoutRequest true "payout"
//	@Success     201             {object} Data[Payout]
//	@Failure     400             {object} Error "VALIDATION_FAILED"
//	@Failure     401             {object} Error "UNAUTHORIZED"
//	@Failure     403             {object} Error "NOT_CREATOR / CHANNEL_SUSPENDED"
//	@Failure     409             {object} Error "INVALID_STATE / CONFLICT"
//	@Failure     422             {object} Error "IDEMPOTENCY_KEY_REUSED"
//	@Router      /studio/monetization/payouts [post]
func (r *routes) requestPayout(ctx *fiber.Ctx) error {
	userID, ok := caller(ctx)
	if !ok {
		return unauthorized(ctx)
	}

	var body PayoutRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, map[string]string{_fieldBody: entity.DetailInvalid})
	}

	var amount *int64
	if n, err := strconv.ParseInt(string(body.Amount), 10, 64); err == nil {
		amount = &n
	}

	p, err := r.uc.Studio.RequestPayout(ctx.UserContext(), userID, usecase.PayoutInput{
		Amount: amount, BankCode: body.BankCode, AccountNumber: body.AccountNumber, AccountName: body.AccountName,
	})
	if err != nil {
		return r.fail(ctx, "requestPayout", err)
	}

	return ctx.Status(http.StatusCreated).JSON(Data[Payout]{Data: NewPayout(p)})
}
