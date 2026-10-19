package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase/billing"
	"github.com/stretchr/testify/require"
)

func TestReconcilePending(t *testing.T) {
	t.Parallel()

	paidState := provider.OrderState{
		Status: provider.StatusPaid, Currency: "VND", TxnID: "txn",
		Amount: provider.Amount{Value: 59000, Present: true, Valid: true},
	}

	tests := []struct {
		name    string
		age     time.Duration // since creation; the order expires 30 min after it
		state   *provider.OrderState
		qerr    error
		status  entity.OrderStatus
		changed int
		cancel  bool
	}{
		{name: "too young to ask", age: time.Minute, state: &paidState, status: entity.OrderPending},
		{name: "paid at the provider", age: 5 * time.Minute, state: &paidState, status: entity.OrderPaid, changed: 1},
		{name: "unpaid, still valid", age: 5 * time.Minute, status: entity.OrderPending},
		{name: "unpaid, expired", age: 31 * time.Minute, status: entity.OrderExpired, changed: 1, cancel: true},
		{
			name: "provider down, within give-up", age: 45 * time.Minute, qerr: &entity.ProviderError{},
			status: entity.OrderPending,
		},
		{
			name: "provider down past give-up", age: 61 * time.Minute, qerr: &entity.ProviderError{},
			status: entity.OrderExpired, changed: 1, cancel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			o := create(t, e, viewerID, tier1ID)
			e.m.orders[o.ID].CreatedAt = now.Add(-tt.age)
			e.m.orders[o.ID].ExpiresAt = now.Add(-tt.age + 30*time.Minute)
			e.psp.queryErr = tt.qerr

			if tt.state != nil {
				st := *tt.state
				st.Invoice = o.InvoiceNumber
				e.psp.state[o.InvoiceNumber] = st
			}

			n, err := e.uc.ReconcilePending(context.Background())
			require.NoError(t, err)
			e.uc.Wait()
			require.Equal(t, tt.changed, n)
			require.Equal(t, tt.status, e.m.orders[o.ID].Status)
			require.Equal(t, tt.cancel, len(e.psp.cancelled) == 1)

			if tt.status == entity.OrderExpired {
				require.Equal(t, entity.FailExpired, *e.m.orders[o.ID].FailureReason)
			}
		})
	}
}

func TestSubscriptionExpiry(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()
	e.m.subs["soon"] = &entity.Subscription{
		ID: "soon", UserID: viewerID, ChannelID: channelID, TierID: tier1ID,
		Status: entity.SubActive, AutoRenew: true, CurrentPeriodStart: now.Add(-entity.Days(27)),
		CurrentPeriodEnd: now.Add(entity.Days(2)),
	}
	e.m.subs["off"] = &entity.Subscription{
		ID: "off", UserID: otherID, ChannelID: channelID, TierID: tier1ID,
		Status: entity.SubCancelledPendingEnd, CurrentPeriodStart: now.Add(-entity.Days(29)),
		CurrentPeriodEnd: now.Add(entity.Days(1)),
	}
	e.m.subs["over"] = &entity.Subscription{
		ID: "over", UserID: ownerID, ChannelID: channelID, TierID: tier1ID,
		Status: entity.SubCancelledPendingEnd, CurrentPeriodStart: now.Add(-entity.Days(31)),
		CurrentPeriodEnd: now.Add(-time.Second),
	}

	reminded, expired, err := e.uc.SubscriptionExpiry(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, reminded, "renewal reminders only for subscriptions that remind")
	require.Equal(t, 1, expired)

	reminded, expired, err = e.uc.SubscriptionExpiry(ctx)
	require.NoError(t, err)
	require.Zero(t, reminded, "one reminder per period")
	require.Zero(t, expired)
	require.Equal(t, entity.SubExpired, e.m.subs["over"].Status)
	require.Equal(t, 1, e.m.countEvents(events.TypeSubscriptionExpiring))
	require.Equal(t, 1, e.m.countEvents(events.TypeSubscriptionExpired))
}

func TestReconcileDaily(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()
	unpaid := create(t, e, viewerID, tier1ID)
	paidOK := payOrderAs(t, e, otherID, tier2ID, 119000)
	payOrderAs(t, e, adminID, tier3ID, 299000) // paid here, absent from the provider's list: MISSING_AT_PROVIDER

	amount := func(v int64) provider.Amount { return provider.Amount{Value: v, Present: true, Valid: true} }
	e.psp.listed = []provider.OrderState{
		{Invoice: unpaid.InvoiceNumber, Status: provider.StatusPaid, Amount: amount(59000), Currency: "VND", TxnID: "x"},
		{Invoice: paidOK.InvoiceNumber, Status: provider.StatusPaid, Amount: amount(1), Currency: "VND"},
		{Invoice: "NXLSTRANGER000", Status: provider.StatusPaid, Amount: amount(1), Currency: "VND"},
		{Invoice: "NXLCANCELLED00", Status: provider.StatusCancelled},
	}

	rep, err := e.uc.ReconcileDaily(ctx, now)
	require.NoError(t, err)
	require.Equal(t, usecase.ReconcileReport{Listed: 4, Applied: 1, Mismatches: 3}, rep)
	require.Equal(t, entity.OrderPaid, e.m.orders[unpaid.ID].Status)

	e.psp.queryErr = &entity.ProviderError{}
	_, err = e.uc.ReconcileDaily(ctx, now)
	require.ErrorIs(t, err, entity.ErrProviderUnavailable)
}

func TestReplicaAndRepublish(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()
	ch := "0199e000-0000-7000-8000-0000000000bb"

	require.NoError(t, e.uc.ChannelCreated(ctx, "ev", now, events.ChannelCreated{
		ChannelID: ch, OwnerID: otherID,
		Handle: "new", DisplayName: "New",
	}))

	tiers, err := e.uc.PublicTiers(ctx, ch)
	require.NoError(t, err)
	require.Len(t, tiers, 3)
	require.Equal(t, []int64{59000, 119000, 299000}, []int64{tiers[0].Price, tiers[1].Price, tiers[2].Price})
	require.Contains(t, tiers[0].Description, "Badge Sub T1")

	require.NoError(t, e.uc.ChannelStatus(ctx, "ev2", now, ch, true))

	_, err = e.uc.PublicTiers(ctx, ch)
	require.ErrorIs(t, err, entity.ErrChannelSuspended)

	payOrder(t, e)

	n, err := e.uc.RepublishEntitlements(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var replays int

	for _, ob := range e.m.outbox {
		if p, ok := ob.Payload.(events.SubscriptionActivated); ok && p.Replay {
			replays++

			require.Nil(t, p.OrderID)
		}
	}

	require.Equal(t, 1, replays)

	_, err = e.uc.RepublishEntitlements(ctx, ptr("bad"))
	require.ErrorIs(t, err, entity.ErrValidation)
}

type fakeControl struct {
	paid    []provider.FakePayment
	verify  bool
	actions []string
}

func (f *fakeControl) VerifyForm([]entity.Field) bool { return f.verify }

func (f *fakeControl) Pay(_ context.Context, in provider.FakePayment) error {
	f.paid = append(f.paid, in)

	return nil
}

func (f *fakeControl) Cancel(_ context.Context, inv string) error {
	f.actions = append(f.actions, "cancel "+inv)

	return nil
}

func (f *fakeControl) Decline(_ context.Context, inv string) error {
	f.actions = append(f.actions, "decline "+inv)

	return nil
}

func TestDevPSP(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := newEnv().uc.FakeOpen(ctx, nil)
	require.ErrorIs(t, err, entity.ErrNotFound, "no fake provider: no page")

	fc := &fakeControl{}
	e := newEnvWith(nil, billing.WithFake(fc))
	o := create(t, e, viewerID, tier1ID)
	fields := []entity.Field{{Name: "order_invoice_number", Value: o.InvoiceNumber}, {Name: "signature", Value: "x"}}

	_, err = e.uc.FakeOpen(ctx, fields)
	require.ErrorIs(t, err, entity.ErrValidation)

	fc.verify = true
	got, err := e.uc.FakeOpen(ctx, fields)
	require.NoError(t, err)
	require.Equal(t, o.ID, got.ID)

	to, err := e.uc.FakePay(ctx, o.InvoiceNumber)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:5173/checkout/result?orderId="+o.ID+"&r=success", to)
	require.Equal(t, int64(59000), fc.paid[0].Amount)
	require.Equal(t, "NexusLive Sub Tier 1 @kaelen 30 ngay", fc.paid[0].Description)

	to, err = e.uc.FakeCancel(ctx, o.InvoiceNumber)
	require.NoError(t, err)
	require.Contains(t, to, "&r=cancel")

	to, err = e.uc.FakeFail(ctx, o.InvoiceNumber)
	require.NoError(t, err)
	require.Contains(t, to, "&r=error")

	_, err = e.uc.FakePay(ctx, "NXLNOPE")
	require.ErrorIs(t, err, entity.ErrNotFound)
}
