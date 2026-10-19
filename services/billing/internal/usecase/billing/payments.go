package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// errStateMoved: a locked order changed status under the lock; never expected.
var errStateMoved = errors.New("order changed under lock")

// movedOn turns a conditional update that matched no row into errStateMoved.
func movedOn(ok bool, err error) error {
	if err != nil {
		return err
	}

	if !ok {
		return errStateMoved
	}

	return nil
}

// paidResult is what applying a payment produced.
type paidResult struct {
	At      time.Time
	Applied string
	Sub     *entity.Subscription
	Method  *entity.PaymentMethod
}

// Notify parses and applies one provider notification (webhook, fake PSP, dev-ipn).
func (uc *UseCase) Notify(ctx context.Context, header http.Header, body []byte) (usecase.IPNResult, error) {
	start := time.Now()

	n, err := uc.p.ParseNotification(header, body)

	var res usecase.IPNResult

	switch {
	case errors.Is(err, entity.ErrUnauthorized):
		res = usecase.IPNUnauthorized
	case err != nil:
		res = usecase.IPNMalformed
	default:
		res, err = uc.handle(ctx, n)
		if err != nil {
			res = usecase.IPNError
		}
	}

	uc.obs.IPN(n.Type, res, time.Since(start))

	return res, err
}

func (uc *UseCase) handle(ctx context.Context, n provider.Notification) (usecase.IPNResult, error) {
	switch n.Kind {
	case provider.KindIgnored:
		return usecase.IPNIgnored, nil
	case provider.KindVoid:
		return uc.applyVoid(ctx, n.Voided())
	case provider.KindPaid:
	}

	if res, err := uc.screenPaid(ctx, n); res != "" || err != nil {
		return res, err
	}

	return uc.applyPaid(ctx, n.Paid(), entity.SourceIPN)
}

// screenPaid runs the §5.4 checks before an ORDER_PAID is applied; "" = go on.
func (uc *UseCase) screenPaid(ctx context.Context, n provider.Notification) (usecase.IPNResult, error) {
	if !n.SentAt.IsZero() && uc.now().Sub(n.SentAt) > entity.IPNMaxAge {
		uc.l.Warn("billing - ipn %s sent at %s is older than %s", n.Invoice, n.SentAt, entity.IPNMaxAge)
	}

	o, err := uc.r.Orders.ByInvoice(ctx, n.Invoice)
	if errors.Is(err, entity.ErrNotFound) {
		uc.l.Error("billing - ipn for unknown invoice %s", n.Invoice)

		return usecase.IPNUnknownInvoice, nil
	}

	if err != nil {
		return "", fmt.Errorf("billing - Notify: %w", err)
	}

	if n.CustomerID != "" && n.CustomerID != o.UserID {
		uc.l.Warn("billing - ipn %s customer_id does not match the order", n.Invoice)
	}

	if !n.Approved() {
		return usecase.IPNNotApproved, nil
	}

	return uc.verify(ctx, n.Invoice)
}

// verify asks the provider before applying when BILLING_SEPAY_IPN_VERIFY_VIA_API is on.
func (uc *UseCase) verify(ctx context.Context, invoice string) (usecase.IPNResult, error) {
	if !uc.s.VerifyViaAPI {
		return "", nil
	}

	st, err := uc.p.QueryOrder(ctx, invoice)
	if err != nil && !errors.Is(err, entity.ErrProviderNotFound) {
		return "", fmt.Errorf("billing - Notify - verify: %w", err)
	}

	if err != nil || st.Status != provider.StatusPaid {
		return usecase.IPNUnverified, nil
	}

	return "", nil
}

// amountMismatch implements BR-H25: VND only, whole amounts equal to the order's.
func amountMismatch(o entity.Order, p provider.PaidOrder) bool {
	if p.Currency != entity.Currency || !p.OrderAmount.Valid || p.OrderAmount.Value != o.Amount {
		return true
	}

	return p.TxnAmount.Present && (!p.TxnAmount.Valid || p.TxnAmount.Value != o.Amount)
}

func paidMethod(o entity.Order, reported string) *entity.PaymentMethod {
	if o.PaymentMethod != nil {
		return o.PaymentMethod
	}

	switch m := entity.PaymentMethod(reported); m {
	case entity.MethodCard, entity.MethodBankTransfer, entity.MethodNapas:
		return &m
	}

	return nil
}

func pspRef(p provider.PaidOrder, o entity.Order) string {
	switch {
	case p.TxnID != "":
		return p.TxnID
	case p.ProviderOrderID != "":
		return "order:" + p.ProviderOrderID
	}

	return "order:" + o.ID
}

// applyPaid moves the order to PAID with its subscription, ledger and events in one transaction (§6.3).
// IPN, sync and the reconcile jobs all end here; the order row lock makes it apply once.
func (uc *UseCase) applyPaid(ctx context.Context, p provider.PaidOrder, source string) (usecase.IPNResult, error) {
	res := usecase.IPNDuplicate

	var done *entity.Order

	err := uc.r.Orders.WithLockedOrder(ctx, p.Invoice, func(tx repo.PaymentTx, o entity.Order) error {
		if !o.Payable() {
			return nil
		}

		if amountMismatch(o, p) {
			res = usecase.IPNMismatch

			return uc.markMismatch(ctx, tx, o, p)
		}

		txnID, inserted, err := tx.InsertTransaction(ctx, entity.Transaction{
			OrderID: o.ID, Type: entity.TxnCharge, Amount: o.Amount, PSPRef: pspRef(p, o), Source: source, Raw: p.Raw,
		})
		if err != nil || !inserted {
			return err
		}

		paid, err := uc.settle(ctx, tx, o, p, txnID, source)
		if err != nil {
			return err
		}

		res, done = usecase.IPNApplied, &o
		done.Status, done.PaymentMethod = entity.OrderPaid, paid.Method

		return nil
	})
	if errors.Is(err, entity.ErrNotFound) {
		uc.l.Error("billing - payment for unknown invoice %s", p.Invoice)

		return usecase.IPNUnknownInvoice, nil
	}

	if err != nil {
		return "", fmt.Errorf("billing - applyPaid: %w", err)
	}

	if done != nil {
		uc.obs.OrderStatus(entity.OrderPaid, methodLabel(done.PaymentMethod), "")
		uc.l.Info("billing - event=subscribe_complete orderId=%s invoiceNumber=%s userId=%s channelId=%s source=%s",
			done.ID, done.InvoiceNumber, done.UserID, done.ChannelID, source)
	}

	return res, nil
}

// settle writes everything a captured payment changes once the transaction row is in.
func (uc *UseCase) settle(ctx context.Context, tx repo.PaymentTx, o entity.Order, p provider.PaidOrder, txnID, source string,
) (paidResult, error) {
	paid := paidResult{At: uc.now(), Method: paidMethod(o, p.Method)}

	applied, err := subscriptionApplier{}.Apply(ctx, tx, o, paid.At)
	if err != nil {
		return paid, err
	}

	paid.Applied, paid.Sub = applied.Kind, applied.Sub

	var subID, providerOrderID *string
	if applied.Sub != nil {
		subID = &applied.Sub.ID
	}

	if p.ProviderOrderID != "" {
		providerOrderID = &p.ProviderOrderID
	}

	ok, err := tx.MarkPaid(ctx, o.ID, repo.MarkPaid{
		PaidAt: paid.At, ProviderOrderID: providerOrderID, Method: paid.Method, NeedsReview: applied.NeedsReview,
		SubscriptionID: subID,
	})
	if err := movedOn(ok, err); err != nil {
		return paid, err
	}

	fee := entity.Fee(o.Amount, uc.s.FeeBps)
	entries := []entity.LedgerEntry{{
		ChannelID: o.ChannelID, TransactionID: &txnID, Direction: entity.Credit, Kind: entity.LedgerSubRevenue, Amount: o.Amount,
	}}

	if fee > 0 {
		entries = append(entries, entity.LedgerEntry{
			ChannelID: o.ChannelID, TransactionID: &txnID, Direction: entity.Debit, Kind: entity.LedgerPlatformFee, Amount: fee,
		})
	}

	if err := tx.InsertLedger(ctx, entries); err != nil {
		return paid, err
	}

	out := slices.Concat(applied.Events, []repo.Outbox{
		paymentSucceeded(o, paid, source),
		orderAudit(o, "PAYMENT_CAPTURED", map[string]any{
			"amount": o.Amount, keyInvoice: o.InvoiceNumber, "source": source, "late": o.Status != entity.OrderPending,
			"applied": applied.Kind,
		}),
	})

	return paid, tx.InsertOutbox(ctx, out)
}

func (uc *UseCase) markMismatch(ctx context.Context, tx repo.PaymentTx, o entity.Order, p provider.PaidOrder) error {
	if _, err := tx.MarkMismatch(ctx, o.ID); err != nil {
		return err
	}

	uc.l.Warn("billing - amount mismatch on %s", o.InvoiceNumber)

	out := []repo.Outbox{orderAudit(o, "PAYMENT_AMOUNT_MISMATCH", map[string]any{
		"expected": o.Amount, "orderAmount": p.OrderAmount.Value, "transactionAmount": p.TxnAmount.Value,
		"currency": p.Currency, keyInvoice: o.InvoiceNumber,
	})}

	if o.Status == entity.OrderPending {
		failed := o
		failed.Status, failed.FailureReason = entity.OrderFailed, ptr(entity.FailAmountMismatch)
		out = append(out, paymentFailed(failed))

		uc.obs.OrderStatus(entity.OrderFailed, methodLabel(o.PaymentMethod), string(entity.FailAmountMismatch))
	}

	return tx.InsertOutbox(ctx, out)
}

// applyVoid reverses a paid order (§6.4).
func (uc *UseCase) applyVoid(ctx context.Context, v provider.VoidedOrder) (usecase.IPNResult, error) {
	res := usecase.IPNDuplicate

	err := uc.r.Orders.WithLockedOrder(ctx, v.Invoice, func(tx repo.PaymentTx, o entity.Order) error {
		switch o.Status {
		case entity.OrderVoided:
			return nil
		case entity.OrderPaid:
		case entity.OrderPending, entity.OrderCancelled, entity.OrderFailed, entity.OrderExpired:
			res = usecase.IPNVoidIgnored

			uc.l.Warn("billing - void for %s in status %s ignored", o.InvoiceNumber, o.Status)

			return tx.InsertOutbox(ctx, []repo.Outbox{orderAudit(o, "PAYMENT_VOID_IGNORED", map[string]any{
				keyInvoice: o.InvoiceNumber, "status": o.Status,
			})})
		}

		ref := v.TxnID
		if ref == "" {
			ref = "void:" + o.ID
		}

		txnID, inserted, err := tx.InsertTransaction(ctx, entity.Transaction{
			OrderID: o.ID, Type: entity.TxnVoid, Amount: o.Amount, PSPRef: ref, Source: entity.SourceIPN, Raw: v.Raw,
		})
		if err != nil || !inserted {
			return err
		}

		res = usecase.IPNApplied

		return uc.reverse(ctx, tx, o, txnID)
	})
	if errors.Is(err, entity.ErrNotFound) {
		uc.l.Error("billing - void for unknown invoice %s", v.Invoice)

		return usecase.IPNUnknownInvoice, nil
	}

	if err != nil {
		return "", fmt.Errorf("billing - applyVoid: %w", err)
	}

	if res == usecase.IPNApplied {
		uc.obs.Voided()
	}

	return res, nil
}

func (uc *UseCase) reverse(ctx context.Context, tx repo.PaymentTx, o entity.Order, txnID string) error {
	if err := movedOn(tx.MarkVoided(ctx, o.ID)); err != nil {
		return err
	}

	effect, err := subscriptionApplier{}.Void(ctx, tx, o, uc.now())
	if err != nil {
		return err
	}

	// The fee rate of the charge, not today's setting.
	fee, err := tx.ChargeFee(ctx, o.ID)
	if err != nil {
		return err
	}

	entries := []entity.LedgerEntry{{
		ChannelID: o.ChannelID, TransactionID: &txnID, Direction: entity.Debit, Kind: entity.LedgerSubRevenue, Amount: o.Amount,
	}}

	if fee > 0 {
		entries = append(entries, entity.LedgerEntry{
			ChannelID: o.ChannelID, TransactionID: &txnID, Direction: entity.Credit, Kind: entity.LedgerPlatformFee, Amount: fee,
		})
	}

	if err := tx.InsertLedger(ctx, entries); err != nil {
		return err
	}

	out := slices.Concat(effect.Events, []repo.Outbox{
		paymentVoided(o, effect.Sub),
		orderAudit(o, "PAYMENT_VOIDED", map[string]any{keyInvoice: o.InvoiceNumber, "status": entity.OrderVoided}),
	})

	return tx.InsertOutbox(ctx, out)
}

// applied is what an OrderApplier did inside the payment transaction.
type applied struct {
	Kind        string
	NeedsReview bool
	Sub         *entity.Subscription
	Events      []repo.Outbox
}

// subscriptionApplier is the OrderApplier of subscription orders (§2.1): a donation type would add its own.
type subscriptionApplier struct{}

// Apply creates, extends or flags the subscription of a paid order; the order row is already locked.
func (subscriptionApplier) Apply(ctx context.Context, tx repo.PaymentTx, o entity.Order, paidAt time.Time) (applied, error) {
	sub, err := tx.LiveSub(ctx, o.UserID, o.ChannelID)
	if err != nil {
		return applied{}, err
	}

	var out []repo.Outbox

	// A period that ended but was not expired by the job yet must not block another tier.
	if sub != nil && sub.TierID != o.TierID && !sub.CurrentPeriodEnd.After(paidAt) {
		if err := tx.ExpireSub(ctx, sub.ID, sub.CurrentPeriodEnd, events.SubExpiredPeriodEnded); err != nil {
			return applied{}, err
		}

		ended := *sub
		ended.Status, ended.ExpiredReason = entity.SubExpired, ptr(events.SubExpiredPeriodEnded)
		out, sub = append(out, subExpired(ended, events.SubExpiredPeriodEnded)), nil
	}

	switch {
	case sub == nil:
		s, err := tx.InsertSub(ctx, entity.Subscription{
			UserID: o.UserID, ChannelID: o.ChannelID, TierID: o.TierID, CurrentPeriodStart: paidAt,
			CurrentPeriodEnd: paidAt.Add(o.Period()),
		})
		if err != nil {
			return applied{}, err
		}

		return applied{Kind: events.PaymentAppliedActivated, Sub: &s, Events: append(out, subActivated(s, &o.ID, false))}, nil
	case sub.TierID == o.TierID:
		s, err := tx.ExtendSub(ctx, sub.ID, entity.RenewedEnd(sub.CurrentPeriodEnd, paidAt, o.Period()))
		if err != nil {
			return applied{}, err
		}

		return applied{Kind: events.PaymentAppliedRenewed, Sub: &s, Events: append(out, subRenewed(s, o.ID))}, nil
	}

	return applied{Kind: events.PaymentAppliedNeedsReview, NeedsReview: true, Events: append(out,
		orderAudit(o, "PAYMENT_NEEDS_REVIEW", map[string]any{
			keyInvoice: o.InvoiceNumber, "orderTier": o.Tier.Level, "currentTier": sub.Tier.Level,
		}))}, nil
}

// voided is what reversing a payment did to its subscription.
type voided struct {
	Sub    *entity.Subscription
	Events []repo.Outbox
}

// Void takes the voided period off the subscription (§6.4); an order in review never changed one.
func (subscriptionApplier) Void(ctx context.Context, tx repo.PaymentTx, o entity.Order, at time.Time) (voided, error) {
	if o.SubscriptionID == nil || o.NeedsReview {
		return voided{}, nil
	}

	sub, err := tx.Sub(ctx, *o.SubscriptionID)
	if err != nil || sub == nil || sub.Status == entity.SubExpired {
		return voided{Sub: sub}, err
	}

	s := *sub
	if end := s.CurrentPeriodEnd.Add(-o.Period()); end.After(at) {
		if err := tx.ReduceSub(ctx, s.ID, end); err != nil {
			return voided{}, err
		}

		s.CurrentPeriodEnd = end

		return voided{Sub: &s, Events: []repo.Outbox{subUpdated(s, events.SubChangePeriodReduced)}}, nil
	}

	// The GĐ0 CHECK needs end > start.
	end := at
	if minEnd := s.CurrentPeriodStart.Add(time.Millisecond); !end.After(minEnd) {
		end = minEnd
	}

	if err := tx.ExpireSub(ctx, s.ID, end, events.SubExpiredVoided); err != nil {
		return voided{}, err
	}

	s.Status, s.CurrentPeriodEnd, s.ExpiredReason = entity.SubExpired, end, ptr(events.SubExpiredVoided)

	return voided{Sub: &s, Events: []repo.Outbox{subExpired(s, events.SubExpiredVoided)}}, nil
}

func methodLabel(m *entity.PaymentMethod) string {
	if m == nil {
		return "PROVIDER_PAGE"
	}

	return string(*m)
}
