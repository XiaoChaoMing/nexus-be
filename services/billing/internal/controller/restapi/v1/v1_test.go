package v1_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/middleware"
	v1 "github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/require"
)

// fakeUC answers every contract with err (nil = an empty success).
type fakeUC struct {
	usecase.Catalog
	usecase.Subscriptions
	usecase.Admin

	err     error
	created bool
	payout  usecase.PayoutInput
	outcome usecase.Outcome
	orders  usecase.AdminOrderQuery
}

func (f *fakeUC) PublicTiers(context.Context, string) ([]entity.Tier, error) {
	return []entity.Tier{{ID: "t", Level: 1, Price: 59000, Active: true}}, f.err
}

func (f *fakeUC) CreateOrder(context.Context, string, usecase.CreateOrderInput) (entity.Order, entity.Checkout, bool, error) {
	return entity.Order{ID: "o", Amount: 59000, Status: entity.OrderPending}, entity.Checkout{
		Fields: []entity.Field{{Name: "signature", Value: "s"}},
	}, f.created, f.err
}

func (f *fakeUC) GetOrder(context.Context, string, string) (usecase.OrderView, error) {
	return usecase.OrderView{}, f.err
}

func (f *fakeUC) SyncOrder(_ context.Context, _, _ string, o usecase.Outcome) (usecase.OrderView, error) {
	f.outcome = o

	return usecase.OrderView{}, f.err
}

func (f *fakeUC) CancelOrder(context.Context, string, string) (entity.Order, error) {
	return entity.Order{}, f.err
}

func (f *fakeUC) StudioTiers(context.Context, string) ([]usecase.StudioTier, error) {
	return nil, f.err
}

func (f *fakeUC) UpdateTier(context.Context, string, int, usecase.TierPatch) (usecase.StudioTier, error) {
	return usecase.StudioTier{}, f.err
}

func (f *fakeUC) Summary(context.Context, string, string, *time.Location) (entity.Summary, error) {
	return entity.Summary{Subscribers: map[int]int{1: 2, 3: 1}}, f.err
}

func (f *fakeUC) Ledger(context.Context, string, string, int) (usecase.LedgerPage, error) {
	return usecase.LedgerPage{}, f.err
}

func (f *fakeUC) StudioPayouts(context.Context, string, string, int) (usecase.PayoutPage, error) {
	return usecase.PayoutPage{}, f.err
}

func (f *fakeUC) RequestPayout(_ context.Context, _ string, in usecase.PayoutInput) (usecase.PayoutView, error) {
	f.payout = in

	return usecase.PayoutView{Payout: entity.Payout{Number: 7, BankCode: "VCB"}}, f.err
}

func (f *fakeUC) AdminOrders(_ context.Context, q usecase.AdminOrderQuery, _ string, _ int) (usecase.OrderPage, error) {
	f.orders = q

	return usecase.OrderPage{}, f.err
}

func (f *fakeUC) AdminOrder(_ context.Context, ref string) (entity.Order, error) {
	return entity.Order{ID: "o", InvoiceNumber: ref, Status: entity.OrderPaid, UserID: "u2"}, f.err
}

func (f *fakeUC) Begin(context.Context, usecase.IdemKey, []byte) (usecase.IdemState, error) {
	return usecase.IdemState{Outcome: usecase.IdemNew}, nil
}

func (f *fakeUC) Complete(context.Context, usecase.IdemKey, int, []byte) error { return nil }
func (f *fakeUC) Abort(context.Context, usecase.IdemKey) error                 { return nil }

func as(id, role string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", id)
		ctx.Locals("role", role)

		return ctx.Next()
	}
}

func app(uc *fakeUC, role string) *fiber.App {
	a := fiber.New()
	v1.NewRoutes(a.Group("/v1"), v1.UseCases{
		Catalog: uc, Checkout: uc, Subscriptions: uc, Studio: uc, Admin: uc,
		Idempotency: uc,
	}, as("u1", role), middleware.RequireAdmin(), logger.New("error"))

	return a
}

func call(t *testing.T, a *fiber.App, method, path, body string) (status int, reply string) {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "0199f0a3-0000-7000-8000-0000000000aa")

	resp, err := a.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(b)
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err    error
		status int
		code   string
	}{
		{entity.Invalid("tierId", "INVALID"), http.StatusBadRequest, `"VALIDATION_FAILED","message":"validation failed","details":{"tierId":"INVALID"}`},
		{entity.ErrNotFound, http.StatusNotFound, `"NOT_FOUND"`},
		{entity.ErrNotCreator, http.StatusForbidden, `"NOT_CREATOR"`},
		{entity.ErrChannelSuspended, http.StatusForbidden, `"CHANNEL_SUSPENDED"`},
		{entity.ErrSubscribeSelf, http.StatusUnprocessableEntity, `"CANNOT_SUBSCRIBE_SELF"`},
		{
			entity.StateError(entity.ReasonTierChange, "currentTier", "1"), http.StatusConflict,
			`"INVALID_STATE","message":"invalid state","details":{"currentTier":"1","reason":"TIER_CHANGE"}`,
		},
		{entity.ErrPendingLimit, http.StatusConflict, `"CONFLICT","message":"too many pending payments","details":{"limit":"5"}`},
		{&entity.RateLimitError{RetryAfter: 1500 * time.Millisecond}, http.StatusTooManyRequests, `"retryAfterSec":"2"`},
		{context.DeadlineExceeded, http.StatusInternalServerError, `"INTERNAL_ERROR"`},
	}

	for _, tt := range tests {
		status, body := call(t, app(&fakeUC{err: tt.err}, "USER"), http.MethodPost, "/v1/checkout/orders", `{"tierId":"x"}`)
		require.Equal(t, tt.status, status, tt.code)
		require.Contains(t, body, tt.code)
	}
}

func TestRoutes(t *testing.T) { //nolint:funlen // one walk through the routes
	t.Parallel()

	uc := &fakeUC{created: true}
	a := app(uc, "USER")

	status, body := call(t, a, http.MethodPost, "/v1/checkout/orders", `{"tierId":"x"}`)
	require.Equal(t, http.StatusCreated, status)
	require.Contains(t, body, `"fields":[{"name":"signature","value":"s"}]`)
	require.Contains(t, body, `"paymentMethod":null`)

	uc.created = false
	status, _ = call(t, a, http.MethodPost, "/v1/checkout/orders", `{"tierId":"y"}`)
	require.Equal(t, http.StatusOK, status)

	status, body = call(t, a, http.MethodPost, "/v1/checkout/orders/o/sync", `{"outcome":"MAYBE"}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, `"outcome":"ENUM"`)

	status, body = call(t, a, http.MethodPost, "/v1/checkout/orders/o/sync", `{}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, `"outcome":"REQUIRED"`)

	status, _ = call(t, a, http.MethodPost, "/v1/checkout/orders/o/sync", `{"outcome":"CANCEL"}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, usecase.OutcomeCancel, uc.outcome)

	status, body = call(t, a, http.MethodGet, "/v1/channels/c/subscription-tiers", ``)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"nextCursor":null`)
	require.Contains(t, body, `"name":"Tier 1"`)

	status, body = call(t, a, http.MethodGet, "/v1/me/payments?limit=51", ``)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, `"limit"`)

	status, body = call(t, a, http.MethodGet, "/v1/studio/monetization/summary?tz=Mars/Base", ``)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, `"tz":"FORMAT"`)

	status, body = call(t, a, http.MethodGet, "/v1/studio/monetization/summary", ``)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"subscribers":{"tier1":2,"tier2":0,"tier3":1,"total":3}`)

	status, _ = call(t, a, http.MethodPost, "/v1/studio/monetization/payouts", `{"amount":"100000","bankCode":"VCB"}`)
	require.Equal(t, http.StatusCreated, status)
	require.Nil(t, uc.payout.Amount, "a string amount is not an integer")

	status, body = call(t, a, http.MethodPost, "/v1/studio/monetization/payouts", `{"amount":100000}`)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, int64(100000), *uc.payout.Amount)
	require.Contains(t, body, `"code":"PO-0007"`)
	require.Contains(t, body, `"name":"Vietcombank"`)

	status, body = call(t, a, http.MethodPatch, "/v1/studio/monetization/tiers/x", `{"active":true}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, `"level":"ENUM"`)

	// Admin routes need role ADMIN.
	status, body = call(t, a, http.MethodGet, "/v1/admin/payouts", ``)
	require.Equal(t, http.StatusForbidden, status)
	require.Contains(t, body, "FORBIDDEN")
}

func TestAdminOrderRoutes(t *testing.T) {
	t.Parallel()

	uc := &fakeUC{}
	a := app(uc, "ADMIN")

	status, _ := call(t, a, http.MethodGet, "/v1/admin/checkout-orders?status=FAILED&failureReason=AMOUNT_MISMATCH,DECLINED&q=nxl", ``)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{"AMOUNT_MISMATCH", "DECLINED"}, uc.orders.FailureReasons)
	require.Equal(t, []string{"FAILED"}, uc.orders.Statuses)
	require.Equal(t, "nxl", uc.orders.Invoice)

	status, body := call(t, a, http.MethodGet, "/v1/admin/checkout-orders/NXL7Q2K9M4X1C", ``)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"invoiceNumber":"NXL7Q2K9M4X1C"`)
	require.Contains(t, body, `"userId":"u2"`)

	uc.err = entity.ErrNotFound
	status, body = call(t, a, http.MethodGet, "/v1/admin/checkout-orders/nope", ``)
	require.Equal(t, http.StatusNotFound, status)
	require.Contains(t, body, "NOT_FOUND")

	status, _ = call(t, app(uc, "USER"), http.MethodGet, "/v1/admin/checkout-orders/NXL7Q2K9M4X1C", ``)
	require.Equal(t, http.StatusForbidden, status)
}
