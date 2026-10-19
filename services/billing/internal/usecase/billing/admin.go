package billing

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
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
	case st == entity.PayoutRequested || st == entity.PayoutApproved || st == entity.PayoutRejected:
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

	bal, pending, err := uc.r.Ledger.Balance(ctx, p.ChannelID)
	if err != nil {
		return usecase.AdminPayout{}, fmt.Errorf("billing - admin payout balance: %w", err)
	}

	return usecase.AdminPayout{PayoutView: uc.payoutView(p), Channel: ch, Balance: bal, Available: bal - pending}, nil
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

// Approve records the payout as paid out (DEBIT PAYOUT); no money moves (out of scope).
func (uc *UseCase) Approve(ctx context.Context, adminID, id string) (usecase.AdminPayout, error) {
	return uc.review(ctx, adminID, id, entity.PayoutApproved, nil, "PAYOUT_APPROVED")
}

// Reject -; the amount is available again.
func (uc *UseCase) Reject(ctx context.Context, adminID, id, reason string) (usecase.AdminPayout, error) {
	r, err := entity.AdminReason(reason)
	if err != nil {
		return usecase.AdminPayout{}, err
	}

	return uc.review(ctx, adminID, id, entity.PayoutRejected, &r, "PAYOUT_REJECTED")
}

func (uc *UseCase) review(ctx context.Context, adminID, id string, to entity.PayoutStatus, reason *string, action string,
) (usecase.AdminPayout, error) {
	if !validID(id) {
		return usecase.AdminPayout{}, entity.ErrNotFound
	}

	p, err := uc.r.Payouts.Review(ctx, id, adminID, to, reason, uc.now(), func(p entity.Payout) []repo.Outbox {
		return []repo.Outbox{payoutAudit(adminID, events.ActorAdmin, action, p, reason)}
	})
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) || errors.Is(err, entity.ErrInvalidState) {
			return usecase.AdminPayout{}, err
		}

		return usecase.AdminPayout{}, fmt.Errorf("billing - review payout: %w", err)
	}

	uc.obs.PayoutStatus(to)

	return uc.adminPayout(ctx, p)
}

// AdminOrders -: newest first.
func (uc *UseCase) AdminOrders(ctx context.Context, q usecase.AdminOrderQuery, cursor string, limit int,
) (usecase.OrderPage, error) {
	st, err := orderStatuses(q.Statuses)
	if err != nil {
		return usecase.OrderPage{}, err
	}

	if q.Invoice != "" && !_invoicePrefixRe.MatchString(q.Invoice) {
		return usecase.OrderPage{}, entity.Invalid("q", entity.DetailFormat)
	}

	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.OrderPage{}, err
	}

	items, err := uc.r.Orders.Admin(ctx, repo.AdminOrderFilter{
		NeedsReview: q.NeedsReview, Statuses: st,
		InvoicePfx: q.Invoice,
	}, after, limit)
	if err != nil {
		return usecase.OrderPage{}, fmt.Errorf("billing - AdminOrders: %w", err)
	}

	return usecase.OrderPage{Items: items, Next: next(items, limit, orderKey)}, nil
}
