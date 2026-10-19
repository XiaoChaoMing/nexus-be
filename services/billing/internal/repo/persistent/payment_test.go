package persistent_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ipnBody(t *testing.T, typ, invoice, txnID string, amount int64) []byte {
	t.Helper()

	b, err := sepay.BuildIPN(sepay.IPNInput{
		Type: typ, Invoice: invoice, ProviderOrderID: "prov-" + invoice, TxnID: txnID, Amount: amount,
		Method: "BANK_TRANSFER", At: time.Now(),
	})
	require.NoError(t, err)

	return b
}

func secretHeader() http.Header {
	h := http.Header{}
	h.Set(sepay.HeaderSecret, ipnSecret)

	return h
}

// TestConcurrentPaymentAppliesOnce: duplicate IPNs, sync and reconcile racing on one order record exactly one
// transaction, two ledger rows and one subscription.activated (AC-H05).
func TestConcurrentPaymentAppliesOnce(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	_, t1, _ := seedChannel(t, r)
	viewer := newID()
	p := &stubProvider{state: map[string]provider.OrderState{}}
	uc := newUseCase(r, p)

	o, _, created, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: t1.ID, IdempotencyKey: newID()})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, entity.OrderPending, o.Status)
	require.Equal(t, entity.SubActionNew, o.SubAction)

	// The lookup reports no transaction id: reconcile and sync record psp_ref "order:…", the IPN its own id.
	p.state[o.InvoiceNumber] = provider.OrderState{
		Invoice: o.InvoiceNumber, ProviderOrderID: "prov-1", Status: provider.StatusPaid, Currency: "VND",
		Amount: sepay.ParseAmount("59000.00"),
	}
	body := ipnBody(t, sepay.TypeOrderPaid, o.InvoiceNumber, "txn-1", 59000)

	var wg sync.WaitGroup

	for range 6 {
		wg.Go(func() {
			res, err := uc.Notify(ctx, secretHeader(), body)
			assert.NoError(t, err)
			assert.Contains(t, []usecase.IPNResult{usecase.IPNApplied, usecase.IPNDuplicate}, res)
		})
	}

	for range 2 {
		wg.Go(func() {
			_, err := uc.SyncOrder(ctx, viewer, o.ID, usecase.OutcomeSuccess)
			assert.NoError(t, err)
		})
		wg.Go(func() {
			_, err := uc.ReconcilePending(ctx)
			assert.NoError(t, err)
		})
	}

	wg.Wait()

	require.Equal(t, 1, count(t, pg, `SELECT count(*) FROM transactions WHERE order_id = $1`, o.ID))
	require.Equal(t, 1, count(t, pg, `SELECT count(*) FROM ledger_entries WHERE kind = 'SUB_REVENUE' AND direction = 'CREDIT'
		AND amount_vnd = 59000`))
	require.Equal(t, 1, count(t, pg, `SELECT count(*) FROM ledger_entries WHERE kind = 'PLATFORM_FEE' AND direction = 'DEBIT'
		AND amount_vnd = 2950`))
	require.Equal(t, 2, count(t, pg, `SELECT count(*) FROM ledger_entries`))
	require.Equal(t, 1, count(t, pg, `SELECT count(*) FROM subscriptions WHERE user_id = $1 AND status = 'ACTIVE'`, viewer))

	types := outboxTypes(t, pg)
	require.Equal(t, 1, types[events.TypeSubscriptionActivated])
	require.Equal(t, 1, types[events.TypePaymentSucceeded])
	require.Zero(t, types[events.TypePaymentFailed])

	view, err := uc.GetOrder(ctx, viewer, o.ID)
	require.NoError(t, err)
	require.Equal(t, entity.OrderPaid, view.Order.Status)
	require.NotNil(t, view.Subscription)
	require.Equal(t, entity.SubActive, view.Subscription.Status)
	require.WithinDuration(t, view.Order.PaidAt.Add(entity.Days(30)), view.Subscription.CurrentPeriodEnd, time.Second)

	// Concurrent duplicate VOIDs reverse it once.
	void := ipnBody(t, sepay.TypeTransactionVoid, o.InvoiceNumber, "txn-1", 59000)

	for range 4 {
		wg.Go(func() {
			_, err := uc.Notify(ctx, secretHeader(), void)
			assert.NoError(t, err)
		})
	}

	wg.Wait()

	require.Equal(t, 1, count(t, pg, `SELECT count(*) FROM transactions WHERE order_id = $1 AND type = 'VOID'`, o.ID))
	require.Equal(t, 4, count(t, pg, `SELECT count(*) FROM ledger_entries`))
	require.Equal(t, 0, count(t, pg, `SELECT COALESCE(sum(CASE WHEN direction = 'CREDIT' THEN amount_vnd ELSE -amount_vnd END), 0)
		FROM ledger_entries`))
	require.Equal(t, 1, count(t, pg, `SELECT count(*) FROM subscriptions WHERE status = 'EXPIRED' AND expired_reason = 'VOIDED'`))
	types = outboxTypes(t, pg)
	require.Equal(t, 1, types[events.TypePaymentVoided])
	require.Equal(t, 1, types[events.TypeSubscriptionExpired])
}

// TestAppendOnly: transactions and ledger entries refuse UPDATE / DELETE.
func TestAppendOnly(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	_, t1, _ := seedChannel(t, r)
	uc := newUseCase(r, &stubProvider{state: map[string]provider.OrderState{}})

	o, _, _, err := uc.CreateOrder(ctx, newID(), usecase.CreateOrderInput{TierID: t1.ID, IdempotencyKey: newID()})
	require.NoError(t, err)

	res, err := uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, o.InvoiceNumber, "txn-a", 59000))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNApplied, res)

	for _, sql := range []string{
		`UPDATE transactions SET amount_vnd = 1`, `DELETE FROM transactions`,
		`UPDATE ledger_entries SET amount_vnd = 1`, `DELETE FROM ledger_entries`,
	} {
		_, err := pg.Pool.Exec(ctx, sql)
		require.ErrorContains(t, err, "append-only", sql)
	}
}

// TestAmountMismatchAndLateMoney: a wrong amount fails the order for good; money for an expired order wins.
func TestAmountMismatchAndLateMoney(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	_, t1, t2 := seedChannel(t, r)
	viewer := newID()
	uc := newUseCase(r, &stubProvider{state: map[string]provider.OrderState{}})

	o, _, _, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: t1.ID, IdempotencyKey: newID()})
	require.NoError(t, err)

	res, err := uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, o.InvoiceNumber, "txn-m", 5900))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNMismatch, res)

	v, err := uc.GetOrder(ctx, viewer, o.ID)
	require.NoError(t, err)
	require.Equal(t, entity.OrderFailed, v.Order.Status)
	require.Equal(t, entity.FailAmountMismatch, *v.Order.FailureReason)
	require.Zero(t, count(t, pg, `SELECT count(*) FROM subscriptions`))

	// A correct amount afterwards does not revive it.
	res, err = uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, o.InvoiceNumber, "txn-m2", 59000))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNDuplicate, res)

	// Tier 2: cancelled by the viewer, then the money arrives (BR-H20).
	o2, _, _, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: t2.ID, IdempotencyKey: newID()})
	require.NoError(t, err)
	_, err = uc.CancelOrder(ctx, viewer, o2.ID)
	require.NoError(t, err)

	res, err = uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, o2.InvoiceNumber, "txn-late", 119000))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNApplied, res)

	var late bool
	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT (payload->>'late')::boolean FROM outbox
		WHERE event_type = $1`, events.TypePaymentSucceeded).Scan(&late))
	require.True(t, late)
}
