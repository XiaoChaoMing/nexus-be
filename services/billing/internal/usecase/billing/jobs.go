package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

const (
	jobReconcilePending = "reconcile-pending"
	jobSubscriptionExp  = "subscription-expiry"
	jobReconcileDaily   = "reconcile-daily"

	_pendingBatch    = 50
	_expiryBatch     = 500
	_republishBatch  = 500
	_expiryInterval  = time.Minute
	_dailyLockTTL    = 30 * time.Minute
	_dailyHour       = 2
	_dayLayout       = "2006-01-02"
	_lockTTLFactor   = 2
	_resultOK        = "ok"
	_resultError     = "error"
	_resultSkipped   = "skipped"
	mismatchUnknown  = "UNKNOWN_INVOICE"
	mismatchAmount   = "AMOUNT"
	mismatchMissing  = "MISSING_AT_PROVIDER"
	_auditMismatchAc = "PAYMENT_RECONCILE_MISMATCH"
)

// locked runs fn while holding the Redis job lock; another instance holding it → skipped. A Redis error skips
// the run too (the next tick retries).
func (uc *UseCase) locked(ctx context.Context, job string, ttl time.Duration, fn func() error) (bool, error) {
	release, ok, err := uc.r.Lock.TryLock(ctx, job, ttl)
	if err != nil {
		uc.obs.ReconcileRun(job, _resultError)

		return false, fmt.Errorf("billing - %s - lock: %w", job, err)
	}

	if !ok {
		uc.obs.ReconcileRun(job, _resultSkipped)

		return false, nil
	}

	defer release()

	if err := fn(); err != nil {
		uc.obs.ReconcileRun(job, _resultError)

		return true, err
	}

	uc.obs.ReconcileRun(job, _resultOK)

	return true, nil
}

// ReconcilePending asks the provider about orders pending for more than BILLING_RECONCILE_AFTER (§6.9).
func (uc *UseCase) ReconcilePending(ctx context.Context) (int, error) {
	var n int

	_, err := uc.locked(ctx, jobReconcilePending, _lockTTLFactor*uc.s.ReconcileInterval, func() error {
		now := uc.now()

		due, err := uc.r.Orders.DuePending(ctx, now.Add(-uc.s.ReconcileAfter), _pendingBatch)
		if err != nil {
			return fmt.Errorf("billing - ReconcilePending: %w", err)
		}

		for _, o := range due {
			changed, err := uc.reconcileOne(ctx, o, now)
			if err != nil {
				return err
			}

			if changed {
				n++
			}
		}

		return nil
	})

	return n, err
}

func (uc *UseCase) reconcileOne(ctx context.Context, o entity.Order, now time.Time) (bool, error) {
	st, err := uc.p.QueryOrder(ctx, o.InvoiceNumber)

	switch {
	case err == nil && st.Status == provider.StatusPaid:
		res, err := uc.applyPaid(ctx, st.Paid(), entity.SourceReconcile)

		return res == usecase.IPNApplied, err
	case err == nil || errors.Is(err, entity.ErrProviderNotFound):
		if o.ExpiresAt.After(now) {
			return false, nil
		}
	default:
		uc.l.Warn("billing - reconcile %s: %v", o.InvoiceNumber, err)

		if !now.After(o.ExpiresAt.Add(uc.s.ReconcileGiveUp)) {
			return false, nil
		}
	}

	_, cerr := uc.closeOrder(ctx, o, entity.OrderExpired, entity.FailExpired)
	if errors.Is(cerr, entity.ErrInvalidState) {
		return false, nil
	}

	return cerr == nil, cerr
}

// SubscriptionExpiry sends the renewal reminders and expires ended periods (§S8).
func (uc *UseCase) SubscriptionExpiry(ctx context.Context) (reminded, expired int, err error) {
	_, err = uc.locked(ctx, jobSubscriptionExp, _lockTTLFactor*_expiryInterval, func() error {
		var err error

		now := uc.now()
		if reminded, err = uc.remind(ctx, now); err != nil {
			return err
		}

		if expired, err = uc.expire(ctx, now); err != nil {
			return err
		}

		return uc.gauges(ctx, now)
	})

	return reminded, expired, err
}

func (uc *UseCase) remind(ctx context.Context, now time.Time) (int, error) {
	due, err := uc.r.Subs.DueReminders(ctx, now, entity.Days(uc.s.RenewReminderDays), _expiryBatch)
	if err != nil {
		return 0, fmt.Errorf("billing - remind: %w", err)
	}

	n := 0

	for _, s := range due {
		ok, err := uc.r.Subs.MarkReminded(ctx, s, []repo.Outbox{subExpiring(s, s.Renewable(now))})
		if err != nil {
			return n, fmt.Errorf("billing - remind: %w", err)
		}

		if ok {
			n++
		}
	}

	return n, nil
}

func (uc *UseCase) expire(ctx context.Context, now time.Time) (int, error) {
	due, err := uc.r.Subs.DueExpiry(ctx, now, _expiryBatch)
	if err != nil {
		return 0, fmt.Errorf("billing - expire: %w", err)
	}

	n := 0

	for _, s := range due {
		ended := s
		ended.Status = entity.SubExpired

		ok, err := uc.r.Subs.Expire(ctx, s, []repo.Outbox{subExpired(ended, events.SubExpiredPeriodEnded)})
		if err != nil {
			return n, fmt.Errorf("billing - expire: %w", err)
		}

		if ok {
			n++
		}
	}

	return n, nil
}

func (uc *UseCase) gauges(ctx context.Context, now time.Time) error {
	active, err := uc.r.Subs.CountActive(ctx, now)
	if err != nil {
		return fmt.Errorf("billing - gauges: %w", err)
	}

	review, err := uc.r.Orders.CountNeedsReview(ctx)
	if err != nil {
		return fmt.Errorf("billing - gauges: %w", err)
	}

	uc.obs.ActiveSubscriptions(active)
	uc.obs.NeedsReview(review)

	return nil
}

// ReconcileDailyIfDue runs reconcile-daily for yesterday (business time zone) once, after 02:00.
func (uc *UseCase) ReconcileDailyIfDue(ctx context.Context) (bool, usecase.ReconcileReport, error) {
	local := uc.now().In(uc.s.Location)
	if local.Hour() < _dailyHour {
		return false, usecase.ReconcileReport{}, nil
	}

	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, uc.s.Location).AddDate(0, 0, -1)
	key := day.Format(_dayLayout)

	last, err := uc.r.Lock.LastRun(ctx, jobReconcileDaily)
	if err != nil || last >= key {
		return false, usecase.ReconcileReport{}, err
	}

	var rep usecase.ReconcileReport

	ran, err := uc.locked(ctx, jobReconcileDaily, _dailyLockTTL, func() error {
		var err error
		if rep, err = uc.ReconcileDaily(ctx, day); err != nil {
			return err
		}

		return uc.r.Lock.SetLastRun(ctx, jobReconcileDaily, key)
	})

	return ran, rep, err
}

// ReconcileDaily compares the provider's orders of one day with billing's (§6.9): it applies missing
// payments and reports every other difference; it never "fixes" anything else.
func (uc *UseCase) ReconcileDaily(ctx context.Context, day time.Time) (usecase.ReconcileReport, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, uc.s.Location)
	to := from.AddDate(0, 0, 1)

	states, err := uc.p.ListOrders(ctx, from, to)
	if err != nil {
		return usecase.ReconcileReport{}, fmt.Errorf("billing - ReconcileDaily - list: %w", err)
	}

	rep := usecase.ReconcileReport{Listed: len(states)}
	paidAtProvider := map[string]bool{}

	for _, st := range states {
		if st.Status != provider.StatusPaid {
			continue
		}

		paidAtProvider[st.Invoice] = true

		if err := uc.reconcileListed(ctx, st, key(from), &rep); err != nil {
			return rep, err
		}
	}

	local, err := uc.r.Orders.PaidBetween(ctx, from, to)
	if err != nil {
		return rep, fmt.Errorf("billing - ReconcileDaily: %w", err)
	}

	for _, o := range local {
		if o.PSP == uc.p.Name() && !paidAtProvider[o.InvoiceNumber] {
			if err := uc.mismatch(ctx, mismatchMissing, &o, o.InvoiceNumber, key(from), &rep); err != nil {
				return rep, err
			}
		}
	}

	return rep, nil
}

func key(day time.Time) string { return day.Format(_dayLayout) }

func (uc *UseCase) reconcileListed(ctx context.Context, st provider.OrderState, day string, rep *usecase.ReconcileReport) error {
	o, err := uc.r.Orders.ByInvoice(ctx, st.Invoice)
	if errors.Is(err, entity.ErrNotFound) {
		return uc.mismatch(ctx, mismatchUnknown, nil, st.Invoice, day, rep)
	}

	if err != nil {
		return fmt.Errorf("billing - ReconcileDaily: %w", err)
	}

	if o.Status != entity.OrderPaid && o.Status != entity.OrderVoided {
		res, err := uc.applyPaid(ctx, st.Paid(), entity.SourceReconcile)
		if res == usecase.IPNApplied {
			rep.Applied++
		}

		return err
	}

	if st.Amount.Valid && st.Amount.Value != o.Amount {
		return uc.mismatch(ctx, mismatchAmount, &o, o.InvoiceNumber, day, rep)
	}

	return nil
}

func (uc *UseCase) mismatch(ctx context.Context, kind string, o *entity.Order, invoice, day string,
	rep *usecase.ReconcileReport,
) error {
	rep.Mismatches++

	uc.obs.ReconcileMismatch(kind)
	uc.l.Warn("billing - reconcile-daily %s mismatch %s on %s", day, kind, invoice)

	var resID *string
	if o != nil {
		resID = &o.ID
	}

	err := uc.r.Audit.Record(ctx, []repo.Outbox{audit(auditEntry{
		role: events.ActorSystem, action: _auditMismatchAc, aggregate: aggOrder, resType: resOrder, resID: resID,
		meta: map[string]any{"kind": kind, keyInvoice: invoice, "day": day},
	})})
	if err != nil {
		return fmt.Errorf("billing - reconcile mismatch audit: %w", err)
	}

	return nil
}

// PurgeIdempotency drops keys older than 24 h and processed events older than 30 days.
func (uc *UseCase) PurgeIdempotency(ctx context.Context) (int64, error) {
	const processedRetention = 30 * 24 * time.Hour

	now := uc.now()

	n, err := uc.r.Idem.Purge(ctx, now.Add(-entity.IdempotencyTTL))
	if err != nil {
		return n, fmt.Errorf("billing - PurgeIdempotency: %w", err)
	}

	if _, err := uc.r.Idem.PurgeProcessed(ctx, now.Add(-processedRetention)); err != nil {
		return n, fmt.Errorf("billing - PurgeIdempotency: %w", err)
	}

	return n, nil
}

// RepublishEntitlements re-announces every live subscription (replay) so chat-svc rebuilds its copy.
func (uc *UseCase) RepublishEntitlements(ctx context.Context, channelID *string) (int, error) {
	if channelID != nil && !validID(*channelID) {
		return 0, entity.Invalid("channel", entity.DetailInvalid)
	}

	n, after, now := 0, "", uc.now()

	for {
		page, err := uc.r.Subs.Live(ctx, channelID, after, now, _republishBatch)
		if err != nil {
			return n, fmt.Errorf("billing - RepublishEntitlements: %w", err)
		}

		if len(page) == 0 {
			return n, nil
		}

		rows := make([]repo.Outbox, 0, len(page))
		for _, s := range page {
			rows = append(rows, subActivated(s, nil, true))
		}

		if err := uc.r.Audit.Record(ctx, rows); err != nil {
			return n, fmt.Errorf("billing - RepublishEntitlements: %w", err)
		}

		n += len(page)
		after = page[len(page)-1].ID
	}
}
