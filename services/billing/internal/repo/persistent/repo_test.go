package persistent_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelCopy(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	ch := entity.Channel{ID: newID(), OwnerID: newID(), Handle: "old", DisplayName: "Old"}
	eventID, at := newID(), time.Now()

	require.NoError(t, r.UpsertChannel(ctx, eventID, at, ch, false, defaultTiers()))
	// A redelivered event changes nothing.
	ch2 := ch
	ch2.Handle = "changed"
	require.NoError(t, r.UpsertChannel(ctx, eventID, at.Add(time.Second), ch2, false, defaultTiers()))

	got, err := r.Channel(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, "old", got.Handle)
	require.Equal(t, entity.ChannelActive, got.Status)
	require.Equal(t, 3, count(t, pg, `SELECT count(*) FROM subscription_tiers WHERE channel_id = $1`, ch.ID))

	// Newer update applies, an older one does not.
	avatar := "/media/a.webp"
	ch2.AvatarURL = &avatar
	require.NoError(t, r.UpsertChannel(ctx, newID(), at.Add(time.Minute), ch2, true, nil))
	require.NoError(t, r.UpsertChannel(ctx, newID(), at.Add(-time.Minute), ch, true, nil))
	require.NoError(t, r.SetStatus(ctx, newID(), at.Add(2*time.Minute), ch.ID, entity.ChannelSuspended))

	got, err = r.Channel(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, "changed", got.Handle)
	require.Equal(t, &avatar, got.AvatarURL)
	require.True(t, got.Suspended())

	owned, err := r.ByOwner(ctx, ch.OwnerID)
	require.NoError(t, err)
	require.Equal(t, ch.ID, owned.ID)

	_, err = r.ByOwner(ctx, newID())
	require.ErrorIs(t, err, entity.ErrNotCreator)

	_, err = r.Channel(ctx, newID())
	require.ErrorIs(t, err, entity.ErrNotFound)

	// UpdateTier: one audit row per change, none for a no-op.
	desc := "new"
	off := false
	tier, err := r.UpdateTier(ctx, ch.ID, 2, repo.TierPatch{Active: &off, Description: &desc},
		func(_, _ entity.Tier) []repo.Outbox {
			return []repo.Outbox{{AggregateType: "tier", AggregateID: ch.ID, EventType: events.TypeAuditRecorded, Payload: 1}}
		})
	require.NoError(t, err)
	require.False(t, tier.Active)
	require.Equal(t, "new", tier.Description)

	_, err = r.UpdateTier(ctx, ch.ID, 2, repo.TierPatch{Active: &off}, func(_, _ entity.Tier) []repo.Outbox {
		t.Fatal("no change must not write")

		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, outboxTypes(t, pg)[events.TypeAuditRecorded])

	active, err := r.ByChannel(ctx, ch.ID, true)
	require.NoError(t, err)
	require.Len(t, active, 2)
}

func TestCreateOrderRules(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	_, t1, t2 := seedChannel(t, r)
	viewer := newID()
	uc := newUseCase(r, &stubProvider{state: map[string]provider.OrderState{}})
	create := func(tierID string) (entity.Order, bool, error) {
		o, _, created, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: tierID, IdempotencyKey: newID()})

		return o, created, err
	}

	first, created, err := create(t1.ID)
	require.NoError(t, err)
	require.True(t, created)
	require.Len(t, first.InvoiceNumber, 13)

	// Same tier, new key → the pending order (AF-H02); another tier → ORDER_PENDING (AF-H03).
	again, created, err := create(t1.ID)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, again.ID)

	_, _, err = create(t2.ID)
	require.ErrorIs(t, err, entity.ErrInvalidState)
	require.Equal(t, entity.ReasonOrderPending, entity.DetailsOf(err)["reason"])
	require.Equal(t, first.ID, entity.DetailsOf(err)["orderId"])

	// A stale pending order expires on the next checkout (payment.failed) and does not block.
	_, err = pg.Pool.Exec(ctx, `UPDATE checkout_orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, first.ID)
	require.NoError(t, err)

	second, created, err := create(t2.ID)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 1, outboxTypes(t, pg)[events.TypePaymentFailed])

	// Paid tier 2 → tier 1 is TIER_CHANGE, tier 2 again is RENEW.
	res, err := uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, second.InvoiceNumber, "txn-2", 119000))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNApplied, res)

	_, _, err = create(t1.ID)
	require.ErrorIs(t, err, entity.ErrInvalidState)
	require.Equal(t, entity.ReasonTierChange, entity.DetailsOf(err)["reason"])
	require.Equal(t, "2", entity.DetailsOf(err)["currentTier"])

	renew, _, err := create(t2.ID)
	require.NoError(t, err)
	require.Equal(t, entity.SubActionRenew, renew.SubAction)

	// More than 365 days prepaid → MAX_PREPAID.
	_, err = pg.Pool.Exec(ctx, `UPDATE checkout_orders SET status = 'CANCELLED', failure_reason = 'CANCELLED_BY_USER'
		WHERE id = $1`, renew.ID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `UPDATE subscriptions SET current_period_end = now() + interval '340 days'`)
	require.NoError(t, err)

	_, _, err = create(t2.ID)
	require.ErrorIs(t, err, entity.ErrInvalidState)
	require.Equal(t, entity.ReasonMaxPrepaid, entity.DetailsOf(err)["reason"])
}

func TestPendingLimitAndConcurrentCreate(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	viewer := newID()
	uc := newUseCase(r, &stubProvider{state: map[string]provider.OrderState{}})

	_, t1, _ := seedChannel(t, r)

	var wg sync.WaitGroup

	// Two racing checkouts of one channel leave one pending order.
	for range 4 {
		wg.Go(func() {
			_, _, _, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: t1.ID, IdempotencyKey: newID()})
			assert.NoError(t, err)
		})
	}

	wg.Wait()
	require.Equal(t, 1, count(t, pg, `SELECT count(*) FROM checkout_orders WHERE user_id = $1`, viewer))

	for range 4 {
		_, tier, _ := seedChannel(t, r)
		_, _, _, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: tier.ID, IdempotencyKey: newID()})
		require.NoError(t, err)
	}

	_, tier, _ := seedChannel(t, r)
	_, _, created, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: tier.ID, IdempotencyKey: newID()})
	require.ErrorIs(t, err, entity.ErrPendingLimit)
	require.False(t, created)
}

func TestSubscriptionLifecycle(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	_, t1, _ := seedChannel(t, r)
	viewer := newID()
	uc := newUseCase(r, &stubProvider{state: map[string]provider.OrderState{}})

	o, _, _, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: t1.ID, IdempotencyKey: newID()})
	require.NoError(t, err)
	_, err = uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, o.InvoiceNumber, "txn-s", 59000))
	require.NoError(t, err)

	page, err := uc.MySubscriptions(ctx, viewer, usecase.SubQuery{}, "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	sub := page.Items[0]

	// Cancel twice → 409; another user's → 404; resume.
	got, err := uc.CancelSubscription(ctx, viewer, sub.ID)
	require.NoError(t, err)
	require.Equal(t, entity.SubCancelledPendingEnd, got.Status)
	require.False(t, got.AutoRenew)

	_, err = uc.CancelSubscription(ctx, viewer, sub.ID)
	require.ErrorIs(t, err, entity.ErrInvalidState)

	_, err = uc.CancelSubscription(ctx, newID(), sub.ID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	got, err = uc.ResumeSubscription(ctx, viewer, sub.ID)
	require.NoError(t, err)
	require.Equal(t, entity.SubActive, got.Status)

	// Three days left → one reminder per period.
	_, err = pg.Pool.Exec(ctx, `UPDATE subscriptions SET current_period_end = now() + interval '2 days'`)
	require.NoError(t, err)

	for range 2 {
		reminded, expired, err := uc.SubscriptionExpiry(ctx)
		require.NoError(t, err)
		require.Zero(t, expired)
		require.LessOrEqual(t, reminded, 1)
	}

	require.Equal(t, 1, outboxTypes(t, pg)[events.TypeSubscriptionExpiring])

	// Renewal extends from the old end (AC-H15).
	before := count(t, pg, `SELECT extract(epoch FROM current_period_end)::int FROM subscriptions`)
	o2, _, _, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: t1.ID, IdempotencyKey: newID()})
	require.NoError(t, err)
	require.Equal(t, entity.SubActionRenew, o2.SubAction)
	_, err = uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, o2.InvoiceNumber, "txn-r", 59000))
	require.NoError(t, err)

	after := count(t, pg, `SELECT extract(epoch FROM current_period_end)::int FROM subscriptions`)
	require.InDelta(t, before+30*24*3600, after, 1)
	require.Equal(t, 1, outboxTypes(t, pg)[events.TypeSubscriptionRenewed])

	// Period over → EXPIRED once.
	_, err = pg.Pool.Exec(ctx, `UPDATE subscriptions SET current_period_start = now() - interval '61 days', current_period_end = now() - interval '1 second'`)
	require.NoError(t, err)

	_, expired, err := uc.SubscriptionExpiry(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, expired)

	_, expired, err = uc.SubscriptionExpiry(ctx)
	require.NoError(t, err)
	require.Zero(t, expired)

	page, err = uc.MySubscriptions(ctx, viewer, usecase.SubQuery{Status: "expired"}, "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, entity.SubExpired, page.Items[0].Status)

	channelID := sub.ChannelID
	page, err = uc.MySubscriptions(ctx, viewer, usecase.SubQuery{ChannelID: &channelID}, "", 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	payments, err := uc.MyPayments(ctx, viewer, []string{"PAID"}, "", 1)
	require.NoError(t, err)
	require.Len(t, payments.Items, 1)
	require.NotNil(t, payments.Next)

	payments, err = uc.MyPayments(ctx, viewer, []string{"PAID"}, *payments.Next, 1)
	require.NoError(t, err)
	require.Len(t, payments.Items, 1)
	require.NotEqual(t, o2.ID, payments.Items[0].ID)
}

func TestPayouts(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, _ := setup(t)
	ctx := context.Background()
	ch, _, _ := seedChannel(t, r)
	admin := newID()

	uc := newUseCase(r, &stubProvider{state: map[string]provider.OrderState{}})
	viewer := newID()
	tiers, err := r.ByChannel(ctx, ch.ID, true)
	require.NoError(t, err)
	o, _, _, err := uc.CreateOrder(ctx, viewer, usecase.CreateOrderInput{TierID: tiers[2].ID, IdempotencyKey: newID()})
	require.NoError(t, err)
	_, err = uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, o.InvoiceNumber, "txn-p", 299000))
	require.NoError(t, err)

	bal, pending, err := r.Balance(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, int64(284050), bal)
	require.Zero(t, pending)

	mk := func(amount int64) (entity.Payout, error) {
		return r.CreatePayout(ctx, entity.Payout{
			ChannelID: ch.ID, Amount: amount, BankCode: "VCB", AccountLast4: "6789",
			BankEncrypted: []byte{1, 2, 3},
		}, func(entity.Payout) []repo.Outbox { return nil })
	}

	_, err = mk(300000)
	require.ErrorIs(t, err, entity.ErrInvalidState)
	require.Equal(t, "284050", entity.DetailsOf(err)["available"])

	// Concurrent requests: one wins, the others are PAYOUT_PENDING.
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)

	for range 5 {
		wg.Go(func() {
			_, err := mk(100000)

			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				wins++
			} else {
				assert.True(t, errors.Is(err, entity.ErrInvalidState))
			}
		})
	}

	wg.Wait()
	require.Equal(t, 1, wins)

	list, err := r.ListPayouts(ctx, ptr(entity.PayoutRequested), nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, int64(1), list[0].Number)
	require.Equal(t, "PO-0001", list[0].Code())

	approved, err := r.Review(ctx, list[0].ID, admin, entity.PayoutApproved, nil, time.Now(),
		func(entity.Payout) []repo.Outbox { return nil })
	require.NoError(t, err)
	require.Equal(t, entity.PayoutApproved, approved.Status)

	_, err = r.Review(ctx, list[0].ID, admin, entity.PayoutRejected, ptr("x"), time.Now(),
		func(entity.Payout) []repo.Outbox { return nil })
	require.ErrorIs(t, err, entity.ErrInvalidState)

	bal, _, err = r.Balance(ctx, ch.ID)
	require.NoError(t, err)
	require.Equal(t, int64(184050), bal)

	entries, err := r.Entries(ctx, ch.ID, nil, 10)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, entity.LedgerPayout, entries[0].Kind)
	require.Equal(t, "PAYOUT", entries[0].Source)
	require.Equal(t, int64(1), *entries[0].PayoutNumber)
	require.Equal(t, "CHARGE", entries[1].Source)
	require.Equal(t, o.InvoiceNumber, *entries[1].InvoiceNumber)

	totals, err := r.Totals(ctx, ch.ID, time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	require.Equal(t, entity.Totals{Gross: 299000, PlatformFee: 14950}, totals)
}

func TestIdempotencyKeys(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	k := repo.IdemKey{UserID: newID(), Method: "POST", Path: "/v1/checkout/orders", Key: newID()}
	hash, other := []byte("h1"), []byte("h2")

	st, err := r.Begin(ctx, k, hash, time.Minute)
	require.NoError(t, err)
	require.Equal(t, repo.IdemNew, st.Outcome)

	st, err = r.Begin(ctx, k, hash, time.Minute)
	require.NoError(t, err)
	require.Equal(t, repo.IdemInProgress, st.Outcome)

	st, err = r.Begin(ctx, k, other, time.Minute)
	require.NoError(t, err)
	require.Equal(t, repo.IdemMismatch, st.Outcome)

	// A stale unfinished key is taken over.
	_, err = pg.Pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '2 minutes'`)
	require.NoError(t, err)

	st, err = r.Begin(ctx, k, hash, time.Minute)
	require.NoError(t, err)
	require.Equal(t, repo.IdemNew, st.Outcome)

	require.NoError(t, r.Complete(ctx, k, 201, []byte(`{"data":1}`)))

	st, err = r.Begin(ctx, k, hash, time.Minute)
	require.NoError(t, err)
	require.Equal(t, repo.IdemDone, st.Outcome)
	require.Equal(t, 201, st.Status)
	require.JSONEq(t, `{"data":1}`, string(st.Body))

	// Abort frees an unfinished key only.
	k2 := k
	k2.Key = newID()
	_, err = r.Begin(ctx, k2, hash, time.Minute)
	require.NoError(t, err)
	require.NoError(t, r.Abort(ctx, k2))
	require.NoError(t, r.Abort(ctx, k))
	require.Equal(t, 1, count(t, pg, `SELECT count(*) FROM idempotency_keys`))

	n, err := r.Purge(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func ptr[T any](v T) *T { return &v }

func TestAdminOrderFilters(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	_, t1, _ := seedChannel(t, r)
	uc := newUseCase(r, &stubProvider{state: map[string]provider.OrderState{}})

	o, _, _, err := uc.CreateOrder(ctx, newID(), usecase.CreateOrderInput{TierID: t1.ID, IdempotencyKey: newID()})
	require.NoError(t, err)

	res, err := uc.Notify(ctx, secretHeader(), ipnBody(t, sepay.TypeOrderPaid, o.InvoiceNumber, "txn-mismatch", 58000))
	require.NoError(t, err)
	require.Equal(t, usecase.IPNMismatch, res)

	list := func(f repo.AdminOrderFilter) []entity.Order {
		items, err := r.Admin(ctx, f, nil, 20)
		require.NoError(t, err)

		return items
	}

	mismatch := []entity.FailureReason{entity.FailAmountMismatch}
	require.Len(t, list(repo.AdminOrderFilter{FailureReasons: mismatch, Invoice: o.InvoiceNumber, InvoiceExact: true}), 1)

	declined := []entity.FailureReason{entity.FailDeclined}
	require.Empty(t, list(repo.AdminOrderFilter{FailureReasons: declined, Invoice: o.InvoiceNumber, InvoiceExact: true}))
	require.Len(t, list(repo.AdminOrderFilter{Invoice: o.InvoiceNumber[:5]}), count(t, pg,
		`SELECT count(*) FROM checkout_orders WHERE invoice_number LIKE $1 || '%'`, o.InvoiceNumber[:5]))
	require.Empty(t, list(repo.AdminOrderFilter{Invoice: o.InvoiceNumber[:5], InvoiceExact: true}))

	got, err := r.ByID(ctx, o.ID)
	require.NoError(t, err)
	require.Equal(t, entity.OrderFailed, got.Status)
	require.Equal(t, entity.FailAmountMismatch, *got.FailureReason)

	_, err = r.ByID(ctx, newID())
	require.ErrorIs(t, err, entity.ErrNotFound)
}
