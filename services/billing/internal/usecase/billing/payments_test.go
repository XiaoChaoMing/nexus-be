package billing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/require"
)

// ipn builds an IPN body; patch edits the decoded JSON (order / transaction objects) before encoding.
func ipn(t *testing.T, typ, invoice, txnID string, amount int64, patch func(m map[string]any)) []byte {
	t.Helper()

	b, err := sepay.BuildIPN(sepay.IPNInput{
		Type: typ, Invoice: invoice, ProviderOrderID: "prov-" + invoice, TxnID: txnID, Amount: amount,
		Method: "BANK_TRANSFER", CustomerID: viewerID, At: now,
	})
	require.NoError(t, err)

	if patch == nil {
		return b
	}

	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	patch(m)

	out, err := json.Marshal(m)
	require.NoError(t, err)

	return out
}

func header(secret string) http.Header {
	h := http.Header{}
	if secret != "" {
		h.Set(sepay.HeaderSecret, secret)
	}

	return h
}

func setTxn(k string, v any) func(map[string]any) {
	return func(m map[string]any) { m["transaction"].(map[string]any)[k] = v } //nolint:forcetypeassert,errcheck // test body
}

func setOrder(k string, v any) func(map[string]any) {
	return func(m map[string]any) { m["order"].(map[string]any)[k] = v } //nolint:forcetypeassert,errcheck // test body
}

// TestNotifyDecisionTable walks §5.4 row by row.
func TestNotifyDecisionTable(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	tests := []struct {
		name   string
		secret string
		body   func(t *testing.T, inv string) []byte
		res    usecase.IPNResult
		err    error
		status entity.OrderStatus
		reason entity.FailureReason
	}{
		{
			name: "wrong secret", secret: "nope", res: usecase.IPNUnauthorized, err: entity.ErrUnauthorized,
			status: entity.OrderPending, body: paid(59000, nil),
		},
		{
			name: "missing secret", res: usecase.IPNUnauthorized, err: entity.ErrUnauthorized, status: entity.OrderPending,
			body: paid(59000, nil),
		},
		{
			name: "not json", secret: ipnSecret, res: usecase.IPNMalformed, err: entity.ErrMalformed, status: entity.OrderPending,
			body: func(*testing.T, string) []byte { return []byte(`{"order":`) },
		},
		{
			name: "no notification_type", secret: ipnSecret, res: usecase.IPNMalformed, err: entity.ErrMalformed,
			status: entity.OrderPending, body: func(*testing.T, string) []byte { return []byte(`{"order":{}}`) },
		},
		{
			name: "no invoice", secret: ipnSecret, res: usecase.IPNMalformed, err: entity.ErrMalformed,
			status: entity.OrderPending, body: paid(59000, setOrder("order_invoice_number", "")),
		},
		{
			name: "other type", secret: ipnSecret, res: usecase.IPNIgnored, status: entity.OrderPending,
			body: func(*testing.T, string) []byte { return []byte(`{"notification_type":"ORDER_REFUNDED"}`) },
		},
		{
			name: "unknown invoice", secret: ipnSecret, res: usecase.IPNUnknownInvoice, status: entity.OrderPending,
			body: func(t *testing.T, _ string) []byte {
				t.Helper()

				return ipn(t, sepay.TypeOrderPaid, "NXLUNKNOWN0000", "t", 59000, nil)
			},
		},
		{
			name: "not approved", secret: ipnSecret, res: usecase.IPNNotApproved, status: entity.OrderPending,
			body: paid(59000, setTxn("transaction_status", "DECLINED")),
		},
		{
			name: "amount too low", secret: ipnSecret, res: usecase.IPNMismatch, status: entity.OrderFailed,
			reason: entity.FailAmountMismatch, body: paid(58000, nil),
		},
		{
			name: "fractional amount", secret: ipnSecret, res: usecase.IPNMismatch, status: entity.OrderFailed,
			reason: entity.FailAmountMismatch, body: paid(59000, setOrder("order_amount", "59000.50")),
		},
		{
			name: "other currency", secret: ipnSecret, res: usecase.IPNMismatch, status: entity.OrderFailed,
			reason: entity.FailAmountMismatch, body: paid(59000, setOrder("order_currency", "USD")),
		},
		{
			name: "transaction amount differs", secret: ipnSecret, res: usecase.IPNMismatch, status: entity.OrderFailed,
			reason: entity.FailAmountMismatch, body: paid(59000, setTxn("transaction_amount", "5900")),
		},
		{name: "applied", secret: ipnSecret, res: usecase.IPNApplied, status: entity.OrderPaid, body: paid(59000, nil)},
		{
			name: "applied without transaction amount", secret: ipnSecret, res: usecase.IPNApplied, status: entity.OrderPaid,
			body: paid(59000, func(m map[string]any) {
				delete(m["transaction"].(map[string]any), "transaction_amount") //nolint:forcetypeassert,errcheck // test body
			}),
		},
		{
			name: "void of a pending order", secret: ipnSecret, res: usecase.IPNVoidIgnored, status: entity.OrderPending,
			body: func(t *testing.T, inv string) []byte {
				t.Helper()

				return ipn(t, sepay.TypeTransactionVoid, inv, "t", 59000, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			o := create(t, e, viewerID, tier1ID)

			res, err := e.uc.Notify(context.Background(), header(tt.secret), tt.body(t, o.InvoiceNumber))
			require.Equal(t, tt.res, res)

			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}

			v, err := e.uc.GetOrder(context.Background(), viewerID, o.ID)
			require.NoError(t, err)
			require.Equal(t, tt.status, v.Order.Status)

			if tt.reason != "" {
				require.Equal(t, tt.reason, *v.Order.FailureReason)
				require.Equal(t, 1, e.m.countEvents(events.TypePaymentFailed))
				require.Empty(t, e.m.subs)
			}
		})
	}
}

func paid(amount int64, patch func(map[string]any)) func(t *testing.T, inv string) []byte {
	return func(t *testing.T, inv string) []byte {
		t.Helper()

		return ipn(t, sepay.TypeOrderPaid, inv, "txn-"+inv, amount, patch)
	}
}

func TestApplyPaidActivates(t *testing.T) {
	t.Parallel()

	e := newEnv()
	o := create(t, e, viewerID, tier1ID)
	body := ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "txn-1", 59000, nil)

	for i, want := range []usecase.IPNResult{usecase.IPNApplied, usecase.IPNDuplicate, usecase.IPNDuplicate} {
		res, err := e.uc.Notify(context.Background(), header(ipnSecret), body)
		require.NoError(t, err, i)
		require.Equal(t, want, res, i)
	}

	require.Len(t, e.m.txns, 1)
	require.Equal(t, []entity.LedgerEntry{
		{
			ChannelID: channelID, TransactionID: e.m.ledger[0].TransactionID, Direction: entity.Credit,
			Kind: entity.LedgerSubRevenue, Amount: 59000,
		},
		{
			ChannelID: channelID, TransactionID: e.m.ledger[0].TransactionID, Direction: entity.Debit,
			Kind: entity.LedgerPlatformFee, Amount: 2950,
		},
	}, e.m.ledger)
	require.Equal(t, 1, e.m.countEvents(events.TypeSubscriptionActivated))
	require.Equal(t, 1, e.m.countEvents(events.TypePaymentSucceeded))
	require.Equal(t, 1, e.m.countEvents(events.TypeAuditRecorded))

	v, err := e.uc.GetOrder(context.Background(), viewerID, o.ID)
	require.NoError(t, err)
	require.Equal(t, entity.MethodBankTransfer, *v.Order.PaymentMethod)
	require.Equal(t, now.Add(entity.Days(30)), v.Subscription.CurrentPeriodEnd)
	require.True(t, v.Subscription.AutoRenew)
}

func TestApplyPaidToExistingSubscription(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	end := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		sub     entity.Subscription
		applied string
		end     time.Time
		status  entity.SubStatus
		review  bool
		events  []string
	}{
		{
			name: "renewal extends from the old end (AC-H15)", applied: events.PaymentAppliedRenewed,
			sub: entity.Subscription{TierID: tier1ID, Status: entity.SubActive, CurrentPeriodEnd: end},
			end: time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC), status: entity.SubActive,
			events: []string{events.TypeSubscriptionRenewed},
		},
		{
			name: "renewal of a cancelled-at-end subscription resumes it (AF-H04)", applied: events.PaymentAppliedRenewed,
			sub: entity.Subscription{TierID: tier1ID, Status: entity.SubCancelledPendingEnd, CurrentPeriodEnd: end},
			end: time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC), status: entity.SubActive,
			events: []string{events.TypeSubscriptionRenewed},
		},
		{
			name: "ended but not expired yet, same tier: renewed from the payment", applied: events.PaymentAppliedRenewed,
			sub: entity.Subscription{TierID: tier1ID, Status: entity.SubActive, CurrentPeriodEnd: now.Add(-time.Hour)},
			end: now.Add(entity.Days(30)), status: entity.SubActive, events: []string{events.TypeSubscriptionRenewed},
		},
		{
			name: "another tier live: review", applied: events.PaymentAppliedNeedsReview, review: true,
			sub: entity.Subscription{TierID: tier2ID, Status: entity.SubActive, CurrentPeriodEnd: end},
			end: end, status: entity.SubActive, events: []string{events.TypeAuditRecorded},
		},
		{
			name: "another tier ended: expired, new one activated", applied: events.PaymentAppliedActivated,
			sub: entity.Subscription{TierID: tier2ID, Status: entity.SubActive, CurrentPeriodEnd: now.Add(-time.Hour)},
			end: now.Add(-time.Hour), status: entity.SubExpired,
			events: []string{events.TypeSubscriptionExpired, events.TypeSubscriptionActivated},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			// The order predates the subscription (S5: another tier was bought while this one was open).
			o := create(t, e, viewerID, tier1ID)
			s := tt.sub
			s.ID, s.UserID, s.ChannelID, s.CurrentPeriodStart = "sub-1", viewerID, channelID, now.Add(-entity.Days(20))
			e.m.subs[s.ID] = &s

			res, err := e.uc.Notify(context.Background(), header(ipnSecret),
				ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "t", 59000, nil))
			require.NoError(t, err)
			require.Equal(t, usecase.IPNApplied, res)

			require.Equal(t, tt.status, e.m.subs["sub-1"].Status)
			require.Equal(t, tt.end, e.m.subs["sub-1"].CurrentPeriodEnd)
			require.Equal(t, tt.review, e.m.orders[o.ID].NeedsReview)
			require.Len(t, e.m.ledger, 2, "money is in the channel either way")

			for _, ev := range tt.events {
				require.GreaterOrEqual(t, e.m.countEvents(ev), 1, ev)
			}

			var p events.PaymentSucceeded

			for _, ob := range e.m.outbox {
				if ob.EventType == events.TypePaymentSucceeded {
					p = ob.Payload.(events.PaymentSucceeded) //nolint:forcetypeassert,errcheck // built by the use case
				}
			}

			require.Equal(t, tt.applied, p.Applied)
			require.Equal(t, tt.review, p.SubscriptionID == nil)
		})
	}
}

func TestLateMoneyWins(t *testing.T) {
	t.Parallel()

	e := newEnv()
	o := create(t, e, viewerID, tier1ID)

	_, err := e.uc.CancelOrder(context.Background(), viewerID, o.ID)
	require.NoError(t, err)
	e.uc.Wait()

	res, err := e.uc.Notify(context.Background(), header(ipnSecret), ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "t", 59000, nil))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNApplied, res)
	require.Equal(t, entity.OrderPaid, e.m.orders[o.ID].Status)
	require.Nil(t, e.m.orders[o.ID].FailureReason)

	for _, ob := range e.m.outbox {
		if p, ok := ob.Payload.(events.PaymentSucceeded); ok {
			require.True(t, p.Late)
		}
	}
}

func TestVoid(t *testing.T) {
	t.Parallel()

	t.Run("one period: subscription ends", func(t *testing.T) {
		t.Parallel()

		e := newEnv()
		o := create(t, e, viewerID, tier1ID)
		_, err := e.uc.Notify(context.Background(), header(ipnSecret), ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "t", 59000, nil))
		require.NoError(t, err)

		void := ipn(t, sepay.TypeTransactionVoid, o.InvoiceNumber, "t", 59000, nil)

		res, err := e.uc.Notify(context.Background(), header(ipnSecret), void)
		require.NoError(t, err)
		require.Equal(t, usecase.IPNApplied, res)

		res, err = e.uc.Notify(context.Background(), header(ipnSecret), void)
		require.NoError(t, err)
		require.Equal(t, usecase.IPNDuplicate, res)

		require.Equal(t, entity.OrderVoided, e.m.orders[o.ID].Status)

		for _, s := range e.m.subs {
			require.Equal(t, entity.SubExpired, s.Status)
			require.Equal(t, events.SubExpiredVoided, *s.ExpiredReason)
			require.True(t, s.CurrentPeriodEnd.After(s.CurrentPeriodStart))
		}

		bal, _, err := e.m.Balance(context.Background(), channelID)
		require.NoError(t, err)
		require.Zero(t, bal)
		require.Equal(t, 1, e.m.countEvents(events.TypePaymentVoided))
		require.Equal(t, 1, e.m.countEvents(events.TypeSubscriptionExpired))
	})

	t.Run("renewed: one period comes off", func(t *testing.T) {
		t.Parallel()

		e := newEnv()
		end := now.Add(entity.Days(10))
		e.m.subs["sub-1"] = &entity.Subscription{
			ID: "sub-1", UserID: viewerID, ChannelID: channelID, TierID: tier1ID,
			Status: entity.SubActive, CurrentPeriodStart: now.Add(-entity.Days(20)), CurrentPeriodEnd: end,
		}
		o := create(t, e, viewerID, tier1ID)
		_, err := e.uc.Notify(context.Background(), header(ipnSecret), ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "t", 59000, nil))
		require.NoError(t, err)

		res, err := e.uc.Notify(context.Background(), header(ipnSecret),
			ipn(t, sepay.TypeTransactionVoid, o.InvoiceNumber, "t", 59000, nil))
		require.NoError(t, err)
		require.Equal(t, usecase.IPNApplied, res)
		require.Equal(t, entity.SubActive, e.m.subs["sub-1"].Status)
		require.Equal(t, end, e.m.subs["sub-1"].CurrentPeriodEnd)
		require.Equal(t, 1, e.m.countEvents(events.TypeSubscriptionUpdated))
	})
}

func TestVerifyViaAPI(t *testing.T) {
	t.Parallel()

	e := newEnvWith(func(s *settings) { s.VerifyViaAPI = true })
	o := create(t, e, viewerID, tier1ID)
	body := ipn(t, sepay.TypeOrderPaid, o.InvoiceNumber, "t", 59000, nil)

	res, err := e.uc.Notify(context.Background(), header(ipnSecret), body)
	require.NoError(t, err)
	require.Equal(t, usecase.IPNUnverified, res, "the provider does not know the order")

	e.psp.queryErr = &entity.ProviderError{Server: true}
	_, err = e.uc.Notify(context.Background(), header(ipnSecret), body)
	require.ErrorIs(t, err, entity.ErrProviderUnavailable, "500 so SePay retries")
}
