package billing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

const _statusAll = "ALL"

var _invoicePrefixRe = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)

func payoutStatus(s string) (*entity.PayoutStatus, error) {
	switch st := entity.PayoutStatus(s); {
	case s == "":
		return ptr(entity.PayoutRequested), nil
	case s == _statusAll:
		return nil, nil
	case slices.Contains([]entity.PayoutStatus{
		entity.PayoutRequested, entity.PayoutApproved, entity.PayoutProcessing,
		entity.PayoutPaid, entity.PayoutRejected, entity.PayoutFailed,
	}, st):
		return &st, nil
	}

	return nil, entity.Invalid("status", entity.DetailEnum)
}

// AdminPayouts -.
func (uc *UseCase) AdminPayouts(ctx context.Context, status, cursor string, limit int) (usecase.AdminPayoutPage, error) {
	st, err := payoutStatus(status)
	if err != nil {
		return usecase.AdminPayoutPage{}, err
	}

	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.AdminPayoutPage{}, err
	}

	items, err := uc.r.Payouts.ListPayouts(ctx, st, after, limit)
	if err != nil {
		return usecase.AdminPayoutPage{}, fmt.Errorf("billing - AdminPayouts: %w", err)
	}

	out := make([]usecase.AdminPayout, 0, len(items))
	for _, p := range items {
		a, err := uc.adminPayout(ctx, p)
		if err != nil {
			return usecase.AdminPayoutPage{}, err
		}

		out = append(out, a)
	}

	return usecase.AdminPayoutPage{Items: out, Next: next(items, limit, payoutKey)}, nil
}

func (uc *UseCase) adminPayout(ctx context.Context, p entity.Payout) (usecase.AdminPayout, error) {
	ch, err := uc.r.Channels.Channel(ctx, p.ChannelID)
	if err != nil {
		return usecase.AdminPayout{}, fmt.Errorf("billing - admin payout channel: %w", err)
	}

	available, held, err := uc.r.Ledger.Balance(ctx, p.ChannelID)
	if err != nil {
		return usecase.AdminPayout{}, fmt.Errorf("billing - admin payout balance: %w", err)
	}

	return usecase.AdminPayout{
		PayoutView: uc.payoutView(p), Channel: ch, Balance: available + held, Available: available,
	}, nil
}

// PendingCount -.
func (uc *UseCase) PendingCount(ctx context.Context) (int, error) {
	n, err := uc.r.Payouts.CountRequested(ctx)
	if err != nil {
		return 0, fmt.Errorf("billing - PendingCount: %w", err)
	}

	return n, nil
}

func (uc *UseCase) payout(ctx context.Context, id string) (entity.Payout, error) {
	if !validID(id) {
		return entity.Payout{}, entity.ErrNotFound
	}

	p, err := uc.r.Payouts.Payout(ctx, id)
	if err != nil {
		return p, fmt.Errorf("billing - payout: %w", err)
	}

	return p, nil
}

// AdminPayout -.
func (uc *UseCase) AdminPayout(ctx context.Context, id string) (usecase.AdminPayout, error) {
	p, err := uc.payout(ctx, id)
	if err != nil {
		return usecase.AdminPayout{}, err
	}

	return uc.adminPayout(ctx, p)
}

// BankAccount -; every view is audited.
func (uc *UseCase) BankAccount(ctx context.Context, adminID, id string) (entity.BankAccount, error) {
	p, err := uc.payout(ctx, id)
	if err != nil {
		return entity.BankAccount{}, err
	}

	a, err := uc.openAccount(p)
	if err != nil {
		uc.l.Error("billing - payout %s: cannot decrypt bank account", p.ID)

		return a, err
	}

	if err := uc.r.Audit.Record(ctx, []repo.Outbox{audit(auditEntry{
		actorID: &adminID, role: events.ActorAdmin, action: "PAYOUT_BANK_INFO_VIEWED", aggregate: aggPayout,
		resType: resPayout, resID: &p.ID, meta: map[string]any{"channelId": p.ChannelID},
	})}); err != nil {
		return entity.BankAccount{}, fmt.Errorf("billing - BankAccount - audit: %w", err)
	}

	return a, nil
}

// Approve moves a REQUESTED payout to APPROVED and hands it to the payout provider (PROCESSING for the manual
// one). An APPROVED payout is one whose provider call failed: it is handed over again.
func (uc *UseCase) Approve(ctx context.Context, adminID, id string) (usecase.AdminPayout, error) {
	p, err := uc.payout(ctx, id)
	if err != nil {
		return usecase.AdminPayout{}, err
	}

	if p.Status == entity.PayoutRequested {
		if p, err = uc.move(ctx, adminID, id, repo.PayoutMove{To: entity.PayoutApproved}); err != nil {
			return usecase.AdminPayout{}, err
		}
	}

	if p.Status != entity.PayoutApproved {
		return usecase.AdminPayout{}, entity.StateError(entity.ReasonPayoutState, "status", string(p.Status))
	}

	a, err := uc.openAccount(p)
	if err != nil {
		uc.l.Error("billing - payout %s: cannot decrypt bank account", p.ID)

		return usecase.AdminPayout{}, err
	}

	// Outside any transaction: a real provider is a network call and must not hold the row lock.
	res, err := uc.r.Disburser.Disburse(ctx, provider.PayoutInstruction{
		PayoutID: p.ID, Code: p.Code(), Amount: p.Amount, Account: a,
	})
	if err != nil {
		return usecase.AdminPayout{}, fmt.Errorf("billing - Approve - disburse: %w", err)
	}

	p, err = uc.move(ctx, adminID, id, repo.PayoutMove{
		To: res.Status, Provider: ptr(uc.r.Disburser.Name()), Ref: res.Ref, Reason: res.Reason,
	})
	if err != nil {
		return usecase.AdminPayout{}, err
	}

	return uc.adminPayout(ctx, p)
}

// Reject -; the hold is given back.
func (uc *UseCase) Reject(ctx context.Context, adminID, id, reason string) (usecase.AdminPayout, error) {
	r, err := entity.AdminReason(reason)
	if err != nil {
		return usecase.AdminPayout{}, err
	}

	return uc.closePayout(ctx, adminID, id, repo.PayoutMove{To: entity.PayoutRejected, Reason: &r})
}

// MarkPaid records the bank transfer reference of a PROCESSING payout; the held money has left for good.
func (uc *UseCase) MarkPaid(ctx context.Context, adminID, id, reference string) (usecase.AdminPayout, error) {
	ref, err := entity.PayoutReference(reference)
	if err != nil {
		return usecase.AdminPayout{}, err
	}

	return uc.closePayout(ctx, adminID, id, repo.PayoutMove{To: entity.PayoutPaid, Ref: &ref})
}

// MarkFailed closes an APPROVED or PROCESSING payout whose transfer did not go through; the hold is given back.
func (uc *UseCase) MarkFailed(ctx context.Context, adminID, id, reason string) (usecase.AdminPayout, error) {
	r, err := entity.AdminReason(reason)
	if err != nil {
		return usecase.AdminPayout{}, err
	}

	return uc.closePayout(ctx, adminID, id, repo.PayoutMove{To: entity.PayoutFailed, Reason: &r})
}

func (uc *UseCase) closePayout(ctx context.Context, adminID, id string, m repo.PayoutMove) (usecase.AdminPayout, error) {
	if !validID(id) {
		return usecase.AdminPayout{}, entity.ErrNotFound
	}

	p, err := uc.move(ctx, adminID, id, m)
	if err != nil {
		return usecase.AdminPayout{}, err
	}

	return uc.adminPayout(ctx, p)
}

// move applies one state change with its audit row and, when the payout closes, its payout.*.v1 event.
func (uc *UseCase) move(ctx context.Context, adminID, id string, m repo.PayoutMove) (entity.Payout, error) {
	m.ActorID, m.At = adminID, uc.now()

	p, err := uc.r.Payouts.Move(ctx, id, m, func(p entity.Payout) []repo.Outbox {
		out := []repo.Outbox{payoutAudit(adminID, events.ActorAdmin, "PAYOUT_"+string(m.To), p, m.Reason)}
		if e, ok := payoutClosed(p); ok {
			out = append(out, e)
		}

		return out
	})
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) || errors.Is(err, entity.ErrInvalidState) {
			return p, err
		}

		return p, fmt.Errorf("billing - move payout: %w", err)
	}

	uc.obs.PayoutStatus(m.To)

	return p, nil
}

// AdminOrders -: newest first. q is a whole invoice number (exact match) or a prefix of one.
func (uc *UseCase) AdminOrders(ctx context.Context, q usecase.AdminOrderQuery, cursor string, limit int,
) (usecase.OrderPage, error) {
	f, err := adminOrderFilter(q)
	if err != nil {
		return usecase.OrderPage{}, err
	}

	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.OrderPage{}, err
	}

	items, err := uc.r.Orders.Admin(ctx, f, after, limit)
	if err != nil {
		return usecase.OrderPage{}, fmt.Errorf("billing - AdminOrders: %w", err)
	}

	return usecase.OrderPage{Items: items, Next: next(items, limit, orderKey)}, nil
}

func adminOrderFilter(q usecase.AdminOrderQuery) (repo.AdminOrderFilter, error) {
	st, err := orderStatuses(q.Statuses)
	if err != nil {
		return repo.AdminOrderFilter{}, err
	}

	reasons, err := failureReasons(q.FailureReasons)
	if err != nil {
		return repo.AdminOrderFilter{}, err
	}

	if q.Invoice != "" && !_invoicePrefixRe.MatchString(q.Invoice) {
		return repo.AdminOrderFilter{}, entity.Invalid("q", entity.DetailFormat)
	}

	invoice := strings.ToUpper(q.Invoice)

	return repo.AdminOrderFilter{
		NeedsReview: q.NeedsReview, Statuses: st, FailureReasons: reasons,
		Invoice: invoice, InvoiceExact: entity.IsInvoiceNumber(invoice),
	}, nil
}

func failureReasons(in []string) ([]entity.FailureReason, error) {
	out := make([]entity.FailureReason, 0, len(in))

	for _, s := range in {
		switch fr := entity.FailureReason(s); fr {
		case entity.FailCancelledByUser, entity.FailDeclined, entity.FailExpired, entity.FailAmountMismatch,
			entity.FailProviderError, entity.FailUnknown:
			out = append(out, fr)
		default:
			return nil, entity.Invalid("failureReason", entity.DetailEnum)
		}
	}

	return out, nil
}

// AdminOrder -: ref is the order id or its invoice number.
func (uc *UseCase) AdminOrder(ctx context.Context, ref string) (entity.Order, error) {
	var (
		o   entity.Order
		err error
	)

	switch invoice := strings.ToUpper(ref); {
	case validID(ref):
		o, err = uc.r.Orders.ByID(ctx, ref)
	case entity.IsInvoiceNumber(invoice):
		o, err = uc.r.Orders.ByInvoice(ctx, invoice)
	default:
		return entity.Order{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Order{}, fmt.Errorf("billing - AdminOrder: %w", err)
	}

	return o, nil
}
