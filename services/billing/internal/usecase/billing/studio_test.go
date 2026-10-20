package billing_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/require"
)

// payOrder creates and pays a tier-1 order of the viewer.
func payOrder(t *testing.T, e env) entity.Order {
	t.Helper()

	return payOrderAs(t, e, viewerID, tier1ID, 59000)
}

func payOrderAs(t *testing.T, e env, user, tierID string, amount int64) entity.Order {
	t.Helper()

	o := create(t, e, user, tierID)

	res, err := e.uc.Notify(context.Background(), header(ipnSecret), ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "t-"+o.ID,
		amount, nil))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNApplied, res)

	return o
}

func TestSummary(t *testing.T) {
	t.Parallel()

	e := newEnv()
	payOrder(t, e)

	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)

	s, err := e.uc.Summary(context.Background(), ownerID, "", loc)
	require.NoError(t, err)
	require.Equal(t, "7d", s.Range)
	require.Equal(t, int64(59000), s.Gross)
	require.Equal(t, int64(2950), s.PlatformFee)
	require.Equal(t, int64(56050), s.Net)
	require.Equal(t, int64(56050), s.TodayNet)
	require.Equal(t, int64(56050), s.Balance)
	require.Equal(t, int64(56050), s.Available)
	require.Equal(t, 1, s.Subscribers[1])
	// 7 days ending today, starting at 00:00 Vietnam time (17:00 UTC the day before).
	require.Equal(t, time.Date(2026, 2, 3, 17, 0, 0, 0, time.UTC), s.From)

	s, err = e.uc.Summary(context.Background(), ownerID, "28d", time.UTC)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC), s.From)

	_, err = e.uc.Summary(context.Background(), ownerID, "1y", time.UTC)
	require.ErrorIs(t, err, entity.ErrValidation)

	_, err = e.uc.Summary(context.Background(), viewerID, "7d", time.UTC)
	require.ErrorIs(t, err, entity.ErrNotCreator)
}

func TestUpdateTier(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	long := strings.Repeat("é", 201)

	tests := []struct {
		name    string
		user    string
		level   int
		in      usecase.TierPatch
		suspend bool
		err     error
		detail  [2]string
	}{
		{name: "turn tier 2 off", user: ownerID, level: 2, in: usecase.TierPatch{Active: ptr(false)}},
		{name: "describe tier 1", user: ownerID, level: 1, in: usecase.TierPatch{Description: ptr("  Hi  ")}},
		{
			name: "tier 1 cannot be turned off", user: ownerID, level: 1, in: usecase.TierPatch{Active: ptr(false)},
			err: entity.ErrValidation, detail: [2]string{"active", "INVALID"},
		},
		{
			name: "unknown level", user: ownerID, level: 4, in: usecase.TierPatch{Active: ptr(true)},
			err: entity.ErrValidation, detail: [2]string{"level", "ENUM"},
		},
		{name: "empty body", user: ownerID, level: 2, err: entity.ErrValidation, detail: [2]string{"body", "REQUIRED"}},
		{
			name: "description too long", user: ownerID, level: 2, in: usecase.TierPatch{Description: &long},
			err: entity.ErrValidation, detail: [2]string{"description", "LENGTH"},
		},
		{name: "not a creator", user: viewerID, level: 2, in: usecase.TierPatch{Active: ptr(true)}, err: entity.ErrNotCreator},
		{
			name: "suspended", user: ownerID, level: 2, in: usecase.TierPatch{Active: ptr(true)}, suspend: true,
			err: entity.ErrChannelSuspended,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			if tt.suspend {
				c := e.m.channels[channelID]
				c.Status = entity.ChannelSuspended
				e.m.channels[channelID] = c
			}

			got, err := e.uc.UpdateTier(context.Background(), tt.user, tt.level, tt.in)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)

				if tt.detail[0] != "" {
					require.Equal(t, tt.detail[1], entity.DetailsOf(err)[tt.detail[0]])
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.level, got.Tier.Level)
			require.Equal(t, 1, e.m.countEvents(events.TypeAuditRecorded))

			if tt.in.Description != nil {
				require.Equal(t, "Hi", got.Tier.Description)
			}
		})
	}
}

func TestStudioReads(t *testing.T) {
	t.Parallel()

	e := newEnv()
	payOrder(t, e)

	tiers, err := e.uc.StudioTiers(context.Background(), ownerID)
	require.NoError(t, err)
	require.Len(t, tiers, 3)
	require.Equal(t, 1, tiers[0].ActiveSubscribers)

	ledger, err := e.uc.Ledger(context.Background(), ownerID, "", 20)
	require.NoError(t, err)
	require.Len(t, ledger.Items, 2)

	_, err = e.uc.Ledger(context.Background(), ownerID, "%%%", 20)
	require.ErrorIs(t, err, entity.ErrValidation)

	_, err = e.uc.StudioTiers(context.Background(), viewerID)
	require.ErrorIs(t, err, entity.ErrNotCreator)

	_, err = e.uc.Ledger(context.Background(), viewerID, "", 20)
	require.ErrorIs(t, err, entity.ErrNotCreator)

	_, err = e.uc.StudioPayouts(context.Background(), viewerID, "", 20)
	require.ErrorIs(t, err, entity.ErrNotCreator)
}

func TestRequestPayout(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	ok := usecase.PayoutInput{
		Amount: ptr(int64(100000)), BankCode: "VCB", AccountNumber: "0123456789",
		AccountName: "  nguyen   van an ",
	}
	with := func(f func(*usecase.PayoutInput)) usecase.PayoutInput {
		in := ok
		f(&in)

		return in
	}

	tests := []struct {
		name    string
		user    string
		in      usecase.PayoutInput
		balance int64
		open    bool
		suspend bool
		err     error
		detail  [2]string
	}{
		{name: "ok", user: ownerID, in: ok, balance: 284050},
		{
			name: "below minimum", user: ownerID, in: with(func(i *usecase.PayoutInput) { i.Amount = ptr(int64(50000)) }),
			err: entity.ErrValidation, detail: [2]string{"amount", "MIN"},
		},
		{
			name: "above maximum", user: ownerID, in: with(func(i *usecase.PayoutInput) { i.Amount = ptr(int64(2e9)) }),
			err: entity.ErrValidation, detail: [2]string{"amount", "MAX"},
		},
		{
			name: "no amount", user: ownerID, in: with(func(i *usecase.PayoutInput) { i.Amount = nil }),
			err: entity.ErrValidation, detail: [2]string{"amount", "REQUIRED"},
		},
		{
			name: "unknown bank", user: ownerID, in: with(func(i *usecase.PayoutInput) { i.BankCode = "XYZ" }),
			err: entity.ErrValidation, detail: [2]string{"bankCode", "ENUM"},
		},
		{
			name: "account number", user: ownerID, in: with(func(i *usecase.PayoutInput) { i.AccountNumber = "12ab56" }),
			err: entity.ErrValidation, detail: [2]string{"accountNumber", "FORMAT"},
		},
		{
			name: "account name with accents", user: ownerID, in: with(func(i *usecase.PayoutInput) { i.AccountName = "Nguyễn" }),
			err: entity.ErrValidation, detail: [2]string{"accountName", "FORMAT"},
		},
		{
			name: "account name too short", user: ownerID, in: with(func(i *usecase.PayoutInput) { i.AccountName = " a " }),
			err: entity.ErrValidation, detail: [2]string{"accountName", "LENGTH"},
		},
		{
			name: "insufficient balance", user: ownerID, in: ok, balance: 56050, err: entity.ErrInvalidState,
			detail: [2]string{"available", "56050"},
		},
		{
			name: "open request", user: ownerID, in: ok, balance: 500000, open: true, err: entity.ErrInvalidState,
			detail: [2]string{"reason", "PAYOUT_PENDING"},
		},
		{name: "not a creator", user: viewerID, in: ok, err: entity.ErrNotCreator},
		{name: "suspended", user: ownerID, in: ok, suspend: true, err: entity.ErrChannelSuspended},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			e.m.ledger = append(e.m.ledger, entity.LedgerEntry{
				ChannelID: channelID, Direction: entity.Credit,
				Kind: entity.LedgerSubRevenue, Amount: tt.balance,
			})

			if tt.open {
				e.m.payouts["p0"] = &entity.Payout{ID: "p0", ChannelID: channelID, Amount: 1, Status: entity.PayoutRequested}
			}

			if tt.suspend {
				c := e.m.channels[channelID]
				c.Status = entity.ChannelSuspended
				e.m.channels[channelID] = c
			}

			p, err := e.uc.RequestPayout(context.Background(), tt.user, tt.in)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)

				if tt.detail[0] != "" {
					require.Equal(t, tt.detail[1], entity.DetailsOf(err)[tt.detail[0]])
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, "NGUYEN VAN AN", *p.AccountName)
			require.Equal(t, "6789", p.Payout.AccountLast4)
			require.Equal(t, 1, e.m.countEvents(events.TypeAuditRecorded))
		})
	}
}

func TestAdminPayouts(t *testing.T) { //nolint:funlen // one story
	t.Parallel()

	e := newEnv()
	ctx := context.Background()

	e.m.ledger = append(e.m.ledger, entity.LedgerEntry{
		ChannelID: channelID, Direction: entity.Credit,
		Kind: entity.LedgerSubRevenue, Amount: 300000,
	})

	p, err := e.uc.RequestPayout(ctx, ownerID, usecase.PayoutInput{
		Amount: ptr(int64(100000)), BankCode: "TCB",
		AccountNumber: "000111222", AccountName: "An",
	})
	require.NoError(t, err)

	n, err := e.uc.PendingCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	page, err := e.uc.AdminPayouts(ctx, "", "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, int64(300000), page.Items[0].Balance)
	require.Equal(t, int64(200000), page.Items[0].Available)

	_, err = e.uc.AdminPayouts(ctx, "PAID", "", 20)
	require.ErrorIs(t, err, entity.ErrValidation)

	acct, err := e.uc.BankAccount(ctx, adminID, p.Payout.ID)
	require.NoError(t, err)
	require.Equal(t, entity.BankAccount{Code: "TCB", Number: "000111222", Name: "AN"}, acct)

	var viewed bool

	for _, ob := range e.m.outbox {
		if a, ok := ob.Payload.(events.AuditRecorded); ok && a.Action == "PAYOUT_BANK_INFO_VIEWED" {
			viewed = true
		}
	}

	require.True(t, viewed)

	for _, reason := range []string{"", "too short", strings.Repeat("x", 501)} {
		_, err = e.uc.Reject(ctx, adminID, p.Payout.ID, reason)
		require.ErrorIs(t, err, entity.ErrValidation, reason)
	}

	got, err := e.uc.Approve(ctx, adminID, p.Payout.ID)
	require.NoError(t, err)
	require.Equal(t, entity.PayoutApproved, got.Payout.Status)
	require.Equal(t, int64(200000), got.Balance)

	_, err = e.uc.Approve(ctx, adminID, p.Payout.ID)
	require.ErrorIs(t, err, entity.ErrInvalidState)

	_, err = e.uc.Reject(ctx, adminID, p.Payout.ID, "Already paid out by hand")
	require.ErrorIs(t, err, entity.ErrInvalidState)

	_, err = e.uc.Approve(ctx, adminID, missingID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, err = e.uc.AdminPayout(ctx, "bad")
	require.ErrorIs(t, err, entity.ErrNotFound)

	// A blob that does not open: no name in lists, 500 for the full account.
	e.m.payouts[p.Payout.ID].BankEncrypted = []byte("garbage")

	view, err := e.uc.AdminPayout(ctx, p.Payout.ID)
	require.NoError(t, err)
	require.Nil(t, view.AccountName)

	_, err = e.uc.BankAccount(ctx, adminID, p.Payout.ID)
	require.ErrorIs(t, err, entity.ErrDecrypt)
}

func TestAdminOrders(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()
	paid := payOrder(t, e)

	bad := create(t, e, otherID, tier1ID)
	res, err := e.uc.Notify(ctx, header(ipnSecret), ipn(t, sepay.TypeOrderPaid, bad.InvoiceNumber, "t-"+bad.ID, 58000, nil))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNMismatch, res)

	page, err := e.uc.AdminOrders(ctx, usecase.AdminOrderQuery{NeedsReview: ptr(false)}, "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)

	page, err = e.uc.AdminOrders(ctx, usecase.AdminOrderQuery{
		Statuses: []string{"FAILED"}, FailureReasons: []string{"AMOUNT_MISMATCH"},
	}, "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, bad.ID, page.Items[0].ID)

	page, err = e.uc.AdminOrders(ctx, usecase.AdminOrderQuery{FailureReasons: []string{"DECLINED"}}, "", 20)
	require.NoError(t, err)
	require.Empty(t, page.Items)

	// A whole invoice number matches exactly, whatever its case; a shorter q is a prefix.
	page, err = e.uc.AdminOrders(ctx, usecase.AdminOrderQuery{Invoice: strings.ToLower(paid.InvoiceNumber)}, "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, paid.ID, page.Items[0].ID)

	page, err = e.uc.AdminOrders(ctx, usecase.AdminOrderQuery{Invoice: "nxl"}, "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)

	_, err = e.uc.AdminOrders(ctx, usecase.AdminOrderQuery{Invoice: "NXL%"}, "", 20)
	require.ErrorIs(t, err, entity.ErrValidation)

	_, err = e.uc.AdminOrders(ctx, usecase.AdminOrderQuery{Statuses: []string{"LOST"}}, "", 20)
	require.ErrorIs(t, err, entity.ErrValidation)

	_, err = e.uc.AdminOrders(ctx, usecase.AdminOrderQuery{FailureReasons: []string{"LOST"}}, "", 20)
	require.ErrorIs(t, err, entity.ErrValidation)
	require.Equal(t, map[string]string{"failureReason": entity.DetailEnum}, entity.DetailsOf(err))
}

func TestAdminOrder(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()
	o := payOrder(t, e)

	byID, err := e.uc.AdminOrder(ctx, o.ID)
	require.NoError(t, err)
	require.Equal(t, entity.OrderPaid, byID.Status)

	byInvoice, err := e.uc.AdminOrder(ctx, strings.ToLower(o.InvoiceNumber))
	require.NoError(t, err)
	require.Equal(t, o.ID, byInvoice.ID)

	for _, ref := range []string{"NXL0000000000", "0199e000-0000-7000-8000-0000000000ff", "NXL", "x;--"} {
		_, err = e.uc.AdminOrder(ctx, ref)
		require.ErrorIs(t, err, entity.ErrNotFound, ref)
	}
}

func TestSubscriptions(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()
	o := payOrder(t, e)
	subID := *e.m.orders[o.ID].SubscriptionID

	_, err := e.uc.CancelSubscription(ctx, viewerID, "bad")
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, err = e.uc.CancelSubscription(ctx, otherID, subID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, err = e.uc.ResumeSubscription(ctx, viewerID, subID)
	require.ErrorIs(t, err, entity.ErrInvalidState)

	s, err := e.uc.CancelSubscription(ctx, viewerID, subID)
	require.NoError(t, err)
	require.Equal(t, entity.SubCancelledPendingEnd, s.Status)

	_, err = e.uc.CancelSubscription(ctx, viewerID, subID)
	require.ErrorIs(t, err, entity.ErrInvalidState)

	for _, q := range []usecase.SubQuery{{Status: "all"}, {ChannelID: ptr("x")}} {
		_, err = e.uc.MySubscriptions(ctx, viewerID, q, "", 20)
		require.ErrorIs(t, err, entity.ErrValidation)
	}

	ch := channelID
	page, err := e.uc.MySubscriptions(ctx, viewerID, usecase.SubQuery{ChannelID: &ch}, "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	_, err = e.uc.MyPayments(ctx, viewerID, []string{"LOST"}, "", 20)
	require.ErrorIs(t, err, entity.ErrValidation)

	b, err := json.Marshal(e.m.outbox)
	require.NoError(t, err)
	require.Contains(t, string(b), "SUBSCRIPTION_CANCELLED")
}
