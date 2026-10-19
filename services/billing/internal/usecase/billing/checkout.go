package billing

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// PublicTiers -.
func (uc *UseCase) PublicTiers(ctx context.Context, channelID string) ([]entity.Tier, error) {
	if !validID(channelID) {
		return nil, entity.ErrNotFound
	}

	ch, err := uc.r.Channels.Channel(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("billing - PublicTiers: %w", err)
	}

	if ch.Suspended() {
		return nil, entity.ErrChannelSuspended
	}

	tiers, err := uc.r.Tiers.ByChannel(ctx, channelID, true)
	if err != nil {
		return nil, fmt.Errorf("billing - PublicTiers: %w", err)
	}

	return tiers, nil
}

// TierContext -.
func (uc *UseCase) TierContext(ctx context.Context, tierID string) (entity.Channel, []entity.Tier, error) {
	if !validID(tierID) {
		return entity.Channel{}, nil, entity.ErrNotFound
	}

	tier, err := uc.r.Tiers.Tier(ctx, tierID)
	if err != nil {
		return entity.Channel{}, nil, fmt.Errorf("billing - TierContext: %w", err)
	}

	ch, err := uc.r.Channels.Channel(ctx, tier.ChannelID)
	if err != nil {
		return ch, nil, fmt.Errorf("billing - TierContext: %w", err)
	}

	if ch.Suspended() {
		return ch, nil, entity.ErrChannelSuspended
	}

	all, err := uc.r.Tiers.ByChannel(ctx, ch.ID, false)
	if err != nil {
		return ch, nil, fmt.Errorf("billing - TierContext: %w", err)
	}

	tiers := slices.DeleteFunc(all, func(t entity.Tier) bool { return !t.Active && t.ID != tierID })

	return ch, tiers, nil
}

// Config -.
func (uc *UseCase) Config() usecase.CheckoutConfig {
	return usecase.CheckoutConfig{
		Provider: uc.p.Name(), PaymentMethods: slices.Clone(uc.s.Methods), PeriodDays: entity.SubPeriodDays,
		RenewReminderDays: uc.s.RenewReminderDays, MaxPrepaidDays: entity.MaxPrepaidDays, OrderTTL: uc.s.OrderTTL,
		MaxPending: entity.MaxPendingOrders,
	}
}

// method checks the requested method against BILLING_SEPAY_METHODS; nil = choose on the provider page.
func (uc *UseCase) method(m *string) (*entity.PaymentMethod, error) {
	if m == nil || *m == "" {
		return nil, nil
	}

	pm := entity.PaymentMethod(*m)
	if !slices.Contains(uc.s.Methods, pm) {
		return nil, entity.Invalid("paymentMethod", entity.DetailEnum)
	}

	return &pm, nil
}

// CreateOrder runs §6.2.1 in its fixed order and signs the form.
func (uc *UseCase) CreateOrder(ctx context.Context, userID string, in usecase.CreateOrderInput,
) (entity.Order, entity.Checkout, bool, error) {
	method, err := uc.method(in.PaymentMethod)
	if err != nil {
		return entity.Order{}, entity.Checkout{}, false, err
	}

	tier, err := uc.orderableTier(ctx, userID, in.TierID)
	if err != nil {
		return entity.Order{}, entity.Checkout{}, false, err
	}

	now := uc.now()

	o, created, err := uc.r.Orders.CreateOrReuse(ctx, entity.Order{
		UserID: userID, ChannelID: tier.ChannelID, TierID: tier.ID, Amount: tier.Price, PSP: uc.p.Name(),
		PeriodDays: entity.SubPeriodDays, PaymentMethod: method, IdempotencyKey: in.IdempotencyKey,
		ExpiresAt: now.Add(uc.s.OrderTTL),
	}, repo.CreateRules{
		Now: now, MaxPending: entity.MaxPendingOrders, MaxPrepaid: entity.Days(entity.MaxPrepaidDays),
		NewInvoice: func() (string, error) { return entity.NewInvoiceNumber(uc.rand) },
		Expired:    func(o entity.Order) []repo.Outbox { return []repo.Outbox{paymentFailed(o)} },
	})
	if err != nil {
		if !errors.Is(err, entity.ErrInvalidState) && !errors.Is(err, entity.ErrPendingLimit) {
			err = fmt.Errorf("billing - CreateOrder: %w", err)
		}

		return entity.Order{}, entity.Checkout{}, false, err
	}

	c, err := uc.checkout(ctx, o)
	if err != nil {
		return o, c, false, err
	}

	if created {
		uc.obs.OrderStatus(entity.OrderPending, methodLabel(o.PaymentMethod), "")
		uc.l.Info("billing - event=subscribe_start orderId=%s invoiceNumber=%s userId=%s channelId=%s", o.ID,
			o.InvoiceNumber, o.UserID, o.ChannelID)
	}

	return o, c, created, nil
}

// orderableTier checks §6.2.1 steps 3–6.
func (uc *UseCase) orderableTier(ctx context.Context, userID, tierID string) (entity.Tier, error) {
	if tierID == "" {
		return entity.Tier{}, entity.Invalid("tierId", entity.DetailRequired)
	}

	if !validID(tierID) {
		return entity.Tier{}, entity.Invalid("tierId", entity.DetailInvalid)
	}

	tier, err := uc.r.Tiers.Tier(ctx, tierID)
	if err != nil {
		return tier, fmt.Errorf("billing - CreateOrder: %w", err)
	}

	ch, err := uc.r.Channels.Channel(ctx, tier.ChannelID)
	if err != nil {
		return tier, fmt.Errorf("billing - CreateOrder: %w", err)
	}

	switch {
	case ch.Suspended():
		return tier, entity.ErrChannelSuspended
	case ch.OwnerID == userID:
		return tier, entity.ErrSubscribeSelf
	case !tier.Active:
		return tier, entity.StateError(entity.ReasonTierInactive)
	}

	return tier, nil
}

// resultURL is the web page the provider redirects to; built from BILLING_WEB_BASE_URL only (no open redirect).
func (uc *UseCase) resultURL(orderID, r string) string {
	return strings.TrimSuffix(uc.s.WebBaseURL, "/") + "/checkout/result?orderId=" + url.QueryEscape(orderID) + "&r=" + r
}

func (uc *UseCase) checkout(ctx context.Context, o entity.Order) (entity.Checkout, error) {
	c, err := uc.p.BuildCheckout(ctx, provider.CheckoutOrder{
		ID: o.ID, Invoice: o.InvoiceNumber, UserID: o.UserID, Amount: o.Amount, Method: o.PaymentMethod,
		Description: entity.OrderDescription(o.Tier.Level, o.Channel.Handle, o.PeriodDays),
		SuccessURL:  uc.resultURL(o.ID, "success"), ErrorURL: uc.resultURL(o.ID, "error"),
		CancelURL: uc.resultURL(o.ID, "cancel"),
	})
	if err != nil {
		return c, fmt.Errorf("billing - checkout: %w", err)
	}

	return c, nil
}

// GetOrder -.
func (uc *UseCase) GetOrder(ctx context.Context, userID, orderID string) (usecase.OrderView, error) {
	o, err := uc.userOrder(ctx, userID, orderID)
	if err != nil {
		return usecase.OrderView{}, err
	}

	return uc.view(ctx, o)
}

func (uc *UseCase) userOrder(ctx context.Context, userID, orderID string) (entity.Order, error) {
	if !validID(orderID) {
		return entity.Order{}, entity.ErrNotFound
	}

	o, err := uc.r.Orders.UserOrder(ctx, userID, orderID)
	if err != nil {
		return o, fmt.Errorf("billing - order: %w", err)
	}

	return o, nil
}

// view adds the order's subscription; an order in review shows the buyer's current one instead.
func (uc *UseCase) view(ctx context.Context, o entity.Order) (usecase.OrderView, error) {
	v := usecase.OrderView{Order: o}

	var err error

	switch {
	case o.SubscriptionID != nil:
		v.Subscription, err = uc.r.Subs.Get(ctx, *o.SubscriptionID)
	case o.NeedsReview:
		v.Subscription, err = uc.r.Subs.Latest(ctx, o.UserID, o.ChannelID, uc.now())
		if v.Subscription != nil && !v.Subscription.Live(uc.now()) {
			v.Subscription = nil
		}
	}

	if err != nil {
		return v, fmt.Errorf("billing - view: %w", err)
	}

	return v, nil
}

// SyncOrder asks the provider about a pending order when the browser comes back (AC-H12). Redirect
// parameters never mark an order paid (BR-H21): only the provider's answer does.
func (uc *UseCase) SyncOrder(ctx context.Context, userID, orderID string, outcome usecase.Outcome) (usecase.OrderView, error) {
	o, err := uc.userOrder(ctx, userID, orderID)
	if err != nil {
		return usecase.OrderView{}, err
	}

	if n, ttl, herr := uc.r.Sync.Hit(ctx, o.ID); herr != nil {
		uc.l.Warn("billing - sync limiter: %v", herr)
	} else if n > entity.SyncPerMinute {
		return usecase.OrderView{}, &entity.RateLimitError{RetryAfter: ttl}
	}

	if o.Status == entity.OrderPending {
		if err := uc.syncPending(ctx, o, outcome); err != nil {
			return usecase.OrderView{}, err
		}

		if o, err = uc.userOrder(ctx, userID, orderID); err != nil {
			return usecase.OrderView{}, err
		}
	}

	return uc.view(ctx, o)
}

func (uc *UseCase) syncPending(ctx context.Context, o entity.Order, outcome usecase.Outcome) error {
	st, qerr := uc.p.QueryOrder(ctx, o.InvoiceNumber)
	if qerr == nil && st.Status == provider.StatusPaid {
		_, err := uc.applyPaid(ctx, st.Paid(), entity.SourceReconcile)

		return err
	}

	if qerr != nil && !errors.Is(qerr, entity.ErrProviderNotFound) {
		uc.l.Warn("billing - sync %s: provider lookup failed: %v", o.InvoiceNumber, qerr)
	}

	var err error

	switch outcome {
	case usecase.OutcomeCancel:
		_, err = uc.closeOrder(ctx, o, entity.OrderCancelled, entity.FailCancelledByUser)
	case usecase.OutcomeError:
		_, err = uc.closeOrder(ctx, o, entity.OrderFailed, failureOf(st, qerr))
	case usecase.OutcomeSuccess:
	}

	// The order moved on meanwhile (paid, expired): sync answers its current state.
	if errors.Is(err, entity.ErrInvalidState) {
		return nil
	}

	return err
}

// failureOf picks the reason of a failed redirect from the lookup (§6.7 status table).
func failureOf(st provider.OrderState, qerr error) entity.FailureReason {
	var perr *entity.ProviderError

	switch {
	case qerr == nil && st.Status == provider.StatusFailed && st.Failure != "":
		return st.Failure
	case errors.As(qerr, &perr) && perr.Server:
		return entity.FailProviderError
	}

	return entity.FailUnknown
}

// closeOrder moves a PENDING order to CANCELLED / FAILED / EXPIRED with payment.failed; false = it moved on.
func (uc *UseCase) closeOrder(ctx context.Context, o entity.Order, to entity.OrderStatus, reason entity.FailureReason,
) (entity.Order, error) {
	out, moved, err := uc.r.Orders.Transition(ctx, o.ID, []entity.OrderStatus{entity.OrderPending}, to, &reason,
		func(o entity.Order) []repo.Outbox { return []repo.Outbox{paymentFailed(o)} })
	if err != nil {
		return out, fmt.Errorf("billing - closeOrder: %w", err)
	}

	if !moved {
		return out, entity.ErrInvalidState
	}

	uc.obs.OrderStatus(to, methodLabel(o.PaymentMethod), string(reason))
	uc.l.Info("billing - event=checkout_failed orderId=%s invoiceNumber=%s userId=%s status=%s reason=%s", o.ID,
		o.InvoiceNumber, o.UserID, to, reason)

	if to != entity.OrderFailed {
		uc.cancelAtProvider(ctx, o.InvoiceNumber)
	}

	return out, nil
}

// CancelOrder -: only a PENDING order.
func (uc *UseCase) CancelOrder(ctx context.Context, userID, orderID string) (entity.Order, error) {
	o, err := uc.userOrder(ctx, userID, orderID)
	if err != nil {
		return o, err
	}

	return uc.closeOrder(ctx, o, entity.OrderCancelled, entity.FailCancelledByUser)
}
