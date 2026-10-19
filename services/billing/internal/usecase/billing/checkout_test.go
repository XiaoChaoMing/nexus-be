package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/require"
)

func create(t *testing.T, e env, user, tierID string) entity.Order {
	t.Helper()

	o, _, _, err := e.uc.CreateOrder(context.Background(), user, usecase.CreateOrderInput{TierID: tierID, IdempotencyKey: "k"})
	require.NoError(t, err)

	return o
}

// mustCreate is create for table preparations (no *testing.T there).
func mustCreate(e env, user, tierID string) entity.Order {
	o, _, _, err := e.uc.CreateOrder(context.Background(), user, usecase.CreateOrderInput{TierID: tierID, IdempotencyKey: "k"})
	if err != nil {
		panic(err)
	}

	return o
}

func TestCreateOrder(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	tests := []struct {
		name    string
		user    string
		tier    string
		method  *string
		prepare func(e env)
		err     error
		details map[string]string
		created bool
	}{
		{name: "new order", user: viewerID, tier: tier1ID, method: ptr("BANK_TRANSFER"), created: true},
		{name: "provider page method", user: viewerID, tier: tier1ID, created: true},
		{
			name: "method not configured", user: viewerID, tier: tier1ID, method: ptr("NAPAS_BANK_TRANSFER"),
			err: entity.ErrValidation, details: map[string]string{"paymentMethod": "ENUM"},
		},
		{name: "no tier", user: viewerID, err: entity.ErrValidation, details: map[string]string{"tierId": "REQUIRED"}},
		{
			name: "tier not a uuid", user: viewerID, tier: "x", err: entity.ErrValidation,
			details: map[string]string{"tierId": "INVALID"},
		},
		{name: "unknown tier", user: viewerID, tier: missingID, err: entity.ErrNotFound},
		{name: "suspended channel", user: viewerID, tier: tier1ID, err: entity.ErrChannelSuspended, prepare: func(e env) {
			c := e.m.channels[channelID]
			c.Status = entity.ChannelSuspended
			e.m.channels[channelID] = c
		}},
		{name: "own channel", user: ownerID, tier: tier1ID, err: entity.ErrSubscribeSelf},
		{
			name: "inactive tier", user: viewerID, tier: tier2ID, err: entity.ErrInvalidState,
			details: map[string]string{"reason": "TIER_INACTIVE"}, prepare: func(e env) {
				tr := e.m.tiers[tier2ID]
				tr.Active = false
				e.m.tiers[tier2ID] = tr
			},
		},
		{name: "pending limit", user: viewerID, tier: tier1ID, err: entity.ErrPendingLimit, prepare: func(e env) {
			for range 5 {
				id := e.m.id()
				e.m.orders[id] = &entity.Order{ID: id, UserID: viewerID, ChannelID: e.m.id(), Status: entity.OrderPending}
			}
		}},
		{
			name: "pending order of another tier", user: viewerID, tier: tier1ID, err: entity.ErrInvalidState,
			details: map[string]string{"reason": "ORDER_PENDING"}, prepare: func(e env) { mustCreate(e, viewerID, tier2ID) },
		},
		{
			name: "live subscription of another tier", user: viewerID, tier: tier1ID, err: entity.ErrInvalidState,
			details: map[string]string{"reason": "TIER_CHANGE"}, prepare: func(e env) {
				e.m.subs["s"] = &entity.Subscription{
					ID: "s", UserID: viewerID, ChannelID: channelID, TierID: tier2ID,
					Status: entity.SubActive, CurrentPeriodEnd: now.Add(time.Hour),
				}
			},
		},
		{
			name: "beyond 365 prepaid days", user: viewerID, tier: tier1ID, err: entity.ErrInvalidState,
			details: map[string]string{"reason": "MAX_PREPAID"}, prepare: func(e env) {
				e.m.subs["s"] = &entity.Subscription{
					ID: "s", UserID: viewerID, ChannelID: channelID, TierID: tier1ID,
					Status: entity.SubActive, CurrentPeriodEnd: now.Add(entity.Days(340)),
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			if tt.prepare != nil {
				tt.prepare(e)
			}

			o, c, created, err := e.uc.CreateOrder(context.Background(), tt.user, usecase.CreateOrderInput{
				TierID: tt.tier, PaymentMethod: tt.method, IdempotencyKey: "k",
			})
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)

				for k, v := range tt.details {
					require.Equal(t, v, entity.DetailsOf(err)[k], k)
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.created, created)
			require.Equal(t, entity.OrderPending, o.Status)
			require.Equal(t, int64(59000), o.Amount)
			require.Equal(t, now.Add(30*time.Minute), o.ExpiresAt)
			require.Equal(t, "signature", c.Fields[len(c.Fields)-1].Name)
			require.Contains(t, c.Fields, entity.Field{
				Name:  "success_url",
				Value: "http://localhost:5173/checkout/result?orderId=" + o.ID + "&r=success",
			})
		})
	}
}

func TestCreateOrderReusesPendingOfSameTier(t *testing.T) {
	t.Parallel()

	e := newEnv()
	first := create(t, e, viewerID, tier1ID)

	again, _, created, err := e.uc.CreateOrder(context.Background(), viewerID, usecase.CreateOrderInput{TierID: tier1ID})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, again.ID)
	require.Equal(t, first.InvoiceNumber, again.InvoiceNumber)
}

func TestSyncOrder(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	tests := []struct {
		name    string
		outcome usecase.Outcome
		state   *provider.OrderState
		qerr    error
		status  entity.OrderStatus
		reason  entity.FailureReason
		cancel  bool
	}{
		{name: "success, not paid yet", outcome: usecase.OutcomeSuccess, status: entity.OrderPending},
		{
			name: "success, paid at provider", outcome: usecase.OutcomeSuccess, status: entity.OrderPaid,
			state: &provider.OrderState{
				Status: provider.StatusPaid, Amount: provider.Amount{Value: 59000, Present: true, Valid: true},
				Currency: "VND", TxnID: "t1",
			},
		},
		{
			name: "cancel wins only when unpaid", outcome: usecase.OutcomeCancel, status: entity.OrderPaid,
			state: &provider.OrderState{
				Status: provider.StatusPaid, Amount: provider.Amount{Value: 59000, Present: true, Valid: true},
				Currency: "VND", TxnID: "t1",
			},
		},
		{
			name: "cancel", outcome: usecase.OutcomeCancel, status: entity.OrderCancelled, reason: entity.FailCancelledByUser,
			cancel: true,
		},
		{
			name: "error, declined", outcome: usecase.OutcomeError, status: entity.OrderFailed, reason: entity.FailDeclined,
			state: &provider.OrderState{Status: provider.StatusFailed, Failure: entity.FailDeclined},
		},
		{
			name: "error, provider 5xx", outcome: usecase.OutcomeError, status: entity.OrderFailed,
			reason: entity.FailProviderError, qerr: &entity.ProviderError{Server: true, Status: 502},
		},
		{
			name: "error, timeout", outcome: usecase.OutcomeError, status: entity.OrderFailed, reason: entity.FailUnknown,
			qerr: &entity.ProviderError{},
		},
		{
			name: "cancel, provider down", outcome: usecase.OutcomeCancel, status: entity.OrderCancelled,
			reason: entity.FailCancelledByUser, qerr: &entity.ProviderError{}, cancel: true,
		},
		{
			name: "success, provider down keeps pending", outcome: usecase.OutcomeSuccess, status: entity.OrderPending,
			qerr: &entity.ProviderError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			o := create(t, e, viewerID, tier1ID)
			e.psp.queryErr = tt.qerr

			if tt.state != nil {
				st := *tt.state
				st.Invoice = o.InvoiceNumber
				e.psp.state[o.InvoiceNumber] = st
			}

			v, err := e.uc.SyncOrder(context.Background(), viewerID, o.ID, tt.outcome)
			require.NoError(t, err)
			e.uc.Wait()
			require.Equal(t, tt.status, v.Order.Status)

			if tt.reason != "" {
				require.Equal(t, tt.reason, *v.Order.FailureReason)
				require.Equal(t, 1, e.m.countEvents(events.TypePaymentFailed))
			}

			if tt.status == entity.OrderPaid {
				require.NotNil(t, v.Subscription)
			}

			require.Equal(t, tt.cancel, len(e.psp.cancelled) == 1)
		})
	}
}

func TestSyncOrderGuards(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()
	o := create(t, e, viewerID, tier1ID)

	_, err := e.uc.SyncOrder(ctx, otherID, o.ID, usecase.OutcomeSuccess)
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, err = e.uc.SyncOrder(ctx, viewerID, "not-a-uuid", usecase.OutcomeSuccess)
	require.ErrorIs(t, err, entity.ErrNotFound)

	// A closed order answers its state without asking the provider.
	_, err = e.uc.CancelOrder(ctx, viewerID, o.ID)
	require.NoError(t, err)

	e.psp.queryErr = &entity.ProviderError{Server: true}
	v, err := e.uc.SyncOrder(ctx, viewerID, o.ID, usecase.OutcomeError)
	require.NoError(t, err)
	require.Equal(t, entity.OrderCancelled, v.Order.Status)

	_, err = e.uc.CancelOrder(ctx, viewerID, o.ID)
	require.ErrorIs(t, err, entity.ErrInvalidState)

	// Calls 2–10 pass, the 11th in the minute is refused.
	for range 10 {
		_, err = e.uc.SyncOrder(ctx, viewerID, o.ID, usecase.OutcomeSuccess)
	}

	var rl *entity.RateLimitError
	require.ErrorAs(t, err, &rl)
	require.Equal(t, 42*time.Second, rl.RetryAfter)
	e.uc.Wait()
}

func TestCatalog(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ctx := context.Background()

	tiers, err := e.uc.PublicTiers(ctx, channelID)
	require.NoError(t, err)
	require.Len(t, tiers, 3)

	_, err = e.uc.PublicTiers(ctx, missingID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, err = e.uc.PublicTiers(ctx, "bad")
	require.ErrorIs(t, err, entity.ErrNotFound)

	tr := e.m.tiers[tier3ID]
	tr.Active = false
	e.m.tiers[tier3ID] = tr

	ch, ctxTiers, err := e.uc.TierContext(ctx, tier3ID)
	require.NoError(t, err)
	require.Equal(t, channelID, ch.ID)
	require.Len(t, ctxTiers, 3)

	_, ctxTiers, err = e.uc.TierContext(ctx, tier1ID)
	require.NoError(t, err)
	require.Len(t, ctxTiers, 2)

	c := e.m.channels[channelID]
	c.Status = entity.ChannelSuspended
	e.m.channels[channelID] = c

	_, err = e.uc.PublicTiers(ctx, channelID)
	require.ErrorIs(t, err, entity.ErrChannelSuspended)

	_, _, err = e.uc.TierContext(ctx, tier1ID)
	require.ErrorIs(t, err, entity.ErrChannelSuspended)

	cfg := e.uc.Config()
	require.Equal(t, 30, cfg.PeriodDays)
	require.Equal(t, 5, cfg.MaxPending)
	require.Equal(t, provider.NameSePay, cfg.Provider)
}
