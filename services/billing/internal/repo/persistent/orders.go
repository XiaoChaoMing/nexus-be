package persistent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

const (
	// A top-up has no channel, tier or sub action: they read as zero values.
	_orderCols = `o.id, o.invoice_number, o.user_id, COALESCE(o.channel_id::text, ''), COALESCE(o.tier_id::text, ''),
		o.subscription_id, o.amount_vnd, o.psp, o.psp_ref, COALESCE(o.sub_action, ''), o.period_days, o.payment_method,
		o.status, o.failure_reason, o.needs_review, o.idempotency_key, o.expires_at, o.created_at, o.paid_at, o.updated_at,
		o.order_type,
		COALESCE(c.id::text, ''), COALESCE(c.owner_id::text, ''), COALESCE(c.handle, ''), COALESCE(c.display_name, ''),
		c.avatar_url, COALESCE(c.status, ''),
		COALESCE(t.id::text, ''), COALESCE(t.channel_id::text, ''), COALESCE(t.level, 0), COALESCE(t.price_vnd, 0),
		COALESCE(t.benefits->>'description', ''), COALESCE(t.active, false), COALESCE(t.updated_at, o.created_at)`
	_orderFrom = ` FROM checkout_orders o LEFT JOIN channels c ON c.id = o.channel_id
		LEFT JOIN subscription_tiers t ON t.id = o.tier_id`
	_invoiceAttempts = 3
	_invoiceKey      = "checkout_orders_invoice_key"
)

func scanOrder(s scanner) (entity.Order, error) {
	var (
		o       entity.Order
		method  *string
		failure *string
		c       entity.Channel
		t       entity.Tier
	)

	err := s.Scan(&o.ID, &o.InvoiceNumber, &o.UserID, &o.ChannelID, &o.TierID, &o.SubscriptionID, &o.Amount, &o.PSP,
		&o.PSPRef, &o.SubAction, &o.PeriodDays, &method, &o.Status, &failure, &o.NeedsReview, &o.IdempotencyKey,
		&o.ExpiresAt, &o.CreatedAt, &o.PaidAt, &o.UpdatedAt, &o.OrderType,
		&c.ID, &c.OwnerID, &c.Handle, &c.DisplayName, &c.AvatarURL, &c.Status,
		&t.ID, &t.ChannelID, &t.Level, &t.Price, &t.Description, &t.Active, &t.UpdatedAt)
	if err != nil {
		return o, err
	}

	if method != nil {
		m := entity.PaymentMethod(*method)
		o.PaymentMethod = &m
	}

	if failure != nil {
		f := entity.FailureReason(*failure)
		o.FailureReason = &f
	}

	o.Channel, o.Tier = c, t

	return o, nil
}

func orderBy(ctx context.Context, q querier, where string, args ...any) (entity.Order, error) {
	o, err := scanOrder(q.QueryRow(ctx, `SELECT `+_orderCols+_orderFrom+` WHERE `+where, args...))

	return o, notFound(err)
}

func statusStrings(s []entity.OrderStatus) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = string(v)
	}

	return out
}

// CreateOrReuse -. The per-user advisory lock serializes a user's checkouts; the partial unique index
// checkout_orders_one_pending_idx backs it up.
func (r *Repo) CreateOrReuse(ctx context.Context, o entity.Order, rules repo.CreateRules) (entity.Order, bool, error) {
	var (
		out     entity.Order
		created bool
	)

	err := r.inTx(ctx, "Repo - CreateOrReuse", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('billing:checkout:' || $1, 0))`,
			o.UserID); err != nil {
			return fmt.Errorf("Repo - CreateOrReuse - lock: %w", err)
		}

		action, err := checkLiveSub(ctx, tx, o, rules)
		if err != nil {
			return err
		}

		o.SubAction = action

		reused, err := reusePending(ctx, tx, o, rules)
		if err != nil || reused != nil {
			if reused != nil {
				out = *reused
			}

			return err
		}

		var pending int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM checkout_orders
			WHERE user_id = $1 AND status = 'PENDING' AND expires_at > $2`, o.UserID, rules.Now).Scan(&pending); err != nil {
			return fmt.Errorf("Repo - CreateOrReuse - count: %w", err)
		}

		if pending >= rules.MaxPending {
			return entity.ErrPendingLimit
		}

		out, err = insertOrder(ctx, tx, o, rules)
		created = err == nil

		return err
	})

	return out, created, err
}

// checkLiveSub applies the live-subscription rules: other tier → TIER_CHANGE, same tier → RENEW within
// the prepaid cap. A top-up has no sub action.
func checkLiveSub(ctx context.Context, tx pgx.Tx, o entity.Order, rules repo.CreateRules) (entity.SubAction, error) {
	if o.Type() == entity.OrderTypeTopup {
		return "", nil
	}

	var (
		tierID string
		level  int
		end    time.Time
	)

	err := tx.QueryRow(ctx, `SELECT s.tier_id, t.level, s.current_period_end FROM subscriptions s
		JOIN subscription_tiers t ON t.id = s.tier_id
		WHERE s.user_id = $1 AND s.channel_id = $2 AND s.status IN `+_liveStatuses+` AND s.current_period_end > $3`,
		o.UserID, o.ChannelID, rules.Now).Scan(&tierID, &level, &end)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.SubActionNew, nil
	}

	if err != nil {
		return "", fmt.Errorf("Repo - CreateOrReuse - live sub: %w", err)
	}

	if tierID != o.TierID {
		return "", entity.StateError(entity.ReasonTierChange, "currentTier", strconv.Itoa(level),
			"currentPeriodEnd", end.UTC().Format(time.RFC3339))
	}

	maxEnd := rules.Now.Add(rules.MaxPrepaid)
	if entity.RenewedEnd(end, rules.Now, o.Period()).After(maxEnd) {
		return "", entity.StateError(entity.ReasonMaxPrepaid, "currentPeriodEnd", end.UTC().Format(time.RFC3339),
			"maxPeriodEnd", maxEnd.UTC().Format(time.RFC3339))
	}

	return entity.SubActionRenew, nil
}

// pendingOf locks the caller's pending order of the same kind: of the channel for a subscription, any top-up.
func pendingOf(ctx context.Context, tx pgx.Tx, o entity.Order) (entity.Order, error) {
	if o.Type() == entity.OrderTypeTopup {
		return scanOrder(tx.QueryRow(ctx, `SELECT `+_orderCols+_orderFrom+`
			WHERE o.user_id = $1 AND o.order_type = 'TOPUP' AND o.status = 'PENDING' FOR UPDATE OF o`, o.UserID))
	}

	return scanOrder(tx.QueryRow(ctx, `SELECT `+_orderCols+_orderFrom+`
		WHERE o.user_id = $1 AND o.channel_id = $2 AND o.status = 'PENDING' FOR UPDATE OF o`, o.UserID, o.ChannelID))
}

// samePending reports whether a pending order is what the caller asks for again (AF-H02); otherwise it is the
// ORDER_PENDING error (AF-H03).
func samePending(p, o entity.Order) error {
	switch {
	case o.Type() == entity.OrderTypeTopup && p.Amount != o.Amount:
		return entity.StateError(entity.ReasonOrderPending, "orderId", p.ID, "amount", strconv.FormatInt(p.Amount, 10))
	case o.Type() == entity.OrderTypeSubscription && p.TierID != o.TierID:
		return entity.StateError(entity.ReasonOrderPending, "orderId", p.ID, "tierLevel", strconv.Itoa(p.Tier.Level))
	}

	return nil
}

// reusePending handles the caller's pending order of the same kind: a stale one expires, the same request is
// returned (AF-H02), another one is ORDER_PENDING (AF-H03).
func reusePending(ctx context.Context, tx pgx.Tx, o entity.Order, rules repo.CreateRules) (*entity.Order, error) {
	p, err := pendingOf(ctx, tx, o)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("Repo - CreateOrReuse - pending: %w", err)
	}

	if !p.ExpiresAt.After(rules.Now) {
		if _, err := tx.Exec(ctx, `UPDATE checkout_orders SET status = 'EXPIRED', failure_reason = 'EXPIRED',
			updated_at = now() WHERE id = $1`, p.ID); err != nil {
			return nil, fmt.Errorf("Repo - CreateOrReuse - expire: %w", err)
		}

		reason := entity.FailExpired
		p.Status, p.FailureReason = entity.OrderExpired, &reason

		return nil, insertOutbox(ctx, tx, rules.Expired(p))
	}

	if err := samePending(p, o); err != nil {
		return nil, err
	}

	if o.PaymentMethod != nil {
		if _, err := tx.Exec(ctx, `UPDATE checkout_orders SET payment_method = $2, updated_at = now() WHERE id = $1`,
			p.ID, string(*o.PaymentMethod)); err != nil {
			return nil, fmt.Errorf("Repo - CreateOrReuse - method: %w", err)
		}

		p.PaymentMethod = o.PaymentMethod
	}

	return &p, nil
}

// insertOrder draws invoice numbers until one is free; each attempt runs in a savepoint.
func insertOrder(ctx context.Context, tx pgx.Tx, o entity.Order, rules repo.CreateRules) (entity.Order, error) {
	var method *string

	if o.PaymentMethod != nil {
		m := string(*o.PaymentMethod)
		method = &m
	}

	for attempt := 1; ; attempt++ {
		invoice, err := rules.NewInvoice()
		if err != nil {
			return o, err
		}

		var id string

		sp, err := tx.Begin(ctx)
		if err != nil {
			return o, fmt.Errorf("Repo - CreateOrReuse - savepoint: %w", err)
		}

		err = sp.QueryRow(ctx, `INSERT INTO checkout_orders (user_id, tier_id, channel_id, amount_vnd, idempotency_key, psp,
				status, expires_at, sub_action, period_days, invoice_number, payment_method, order_type)
			VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', $7, $8, $9, $10, $11, $12) RETURNING id`,
			o.UserID, nullable(o.TierID), nullable(o.ChannelID), o.Amount, o.IdempotencyKey, o.PSP, o.ExpiresAt,
			nullable(string(o.SubAction)), o.PeriodDays, invoice, method, string(o.Type())).Scan(&id)
		if err == nil {
			if err := sp.Commit(ctx); err != nil {
				return o, fmt.Errorf("Repo - CreateOrReuse - release: %w", err)
			}

			return orderBy(ctx, tx, `o.id = $1`, id)
		}

		_ = sp.Rollback(ctx) //nolint:errcheck // the insert error below is what matters

		if !isUnique(err, _invoiceKey) || attempt == _invoiceAttempts {
			return o, fmt.Errorf("Repo - CreateOrReuse - insert: %w", err)
		}
	}
}

// UserOrder -.
func (r *Repo) UserOrder(ctx context.Context, userID, id string) (entity.Order, error) {
	o, err := orderBy(ctx, r.Pool, `o.id = $1 AND o.user_id = $2`, id, userID)
	if err != nil {
		return o, fmt.Errorf("Repo - UserOrder: %w", err)
	}

	return o, nil
}

// ByInvoice -.
func (r *Repo) ByInvoice(ctx context.Context, invoice string) (entity.Order, error) {
	o, err := orderBy(ctx, r.Pool, `o.invoice_number = $1`, invoice)
	if err != nil {
		return o, fmt.Errorf("Repo - ByInvoice: %w", err)
	}

	return o, nil
}

// ByID -.
func (r *Repo) ByID(ctx context.Context, id string) (entity.Order, error) {
	o, err := orderBy(ctx, r.Pool, `o.id = $1`, id)
	if err != nil {
		return o, fmt.Errorf("Repo - ByID: %w", err)
	}

	return o, nil
}

func (r *Repo) orders(ctx context.Context, op, where string, args ...any) ([]entity.Order, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_orderCols+_orderFrom+` WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	out, err := collect(rows, scanOrder)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return out, nil
}

// pageArgs is the keyset condition "(created_at, id) < cursor" when after is set.
func pageArgs(after *repo.Cursor) (cursorSet bool, at time.Time, id string) {
	if after == nil {
		return false, time.Time{}, "00000000-0000-0000-0000-000000000000"
	}

	return true, after.At, after.ID
}

// ForUser -: newest first.
func (r *Repo) ForUser(ctx context.Context, userID string, statuses []entity.OrderStatus, after *repo.Cursor,
	limit int,
) ([]entity.Order, error) {
	set, at, id := pageArgs(after)

	return r.orders(ctx, "Repo - ForUser", `o.user_id = $1 AND (cardinality($2::text[]) = 0 OR o.status = ANY($2))
		AND (NOT $3 OR (o.created_at, o.id) < ($4, $5::uuid)) ORDER BY o.created_at DESC, o.id DESC LIMIT $6`,
		userID, statusStrings(statuses), set, at, id, limit)
}

// Admin -: newest first.
func (r *Repo) Admin(ctx context.Context, f repo.AdminOrderFilter, after *repo.Cursor, limit int) ([]entity.Order, error) {
	set, at, id := pageArgs(after)

	reasons := make([]string, 0, len(f.FailureReasons))
	for _, fr := range f.FailureReasons {
		reasons = append(reasons, string(fr))
	}

	exact, prefix := "", f.Invoice
	if f.InvoiceExact {
		exact, prefix = f.Invoice, ""
	}

	return r.orders(ctx, "Repo - Admin", `($1::boolean IS NULL OR o.needs_review = $1)
		AND (cardinality($2::text[]) = 0 OR o.status = ANY($2))
		AND (cardinality($3::text[]) = 0 OR o.failure_reason = ANY($3))
		AND ($4::text = '' OR o.invoice_number = $4::text)
		AND ($5::text = '' OR o.invoice_number LIKE $5::text || '%')
		AND (NOT $6 OR (o.created_at, o.id) < ($7, $8::uuid)) ORDER BY o.created_at DESC, o.id DESC LIMIT $9`,
		f.NeedsReview, statusStrings(f.Statuses), reasons, exact, prefix, set, at, id, limit)
}

// Transition -.
func (r *Repo) Transition(ctx context.Context, id string, from []entity.OrderStatus, to entity.OrderStatus,
	reason *entity.FailureReason, out func(entity.Order) []repo.Outbox,
) (entity.Order, bool, error) {
	var (
		o     entity.Order
		moved bool
	)

	err := r.inTx(ctx, "Repo - Transition", func(tx pgx.Tx) error {
		var why *string

		if reason != nil {
			s := string(*reason)
			why = &s
		}

		tag, err := tx.Exec(ctx, `UPDATE checkout_orders SET status = $2, failure_reason = $3, updated_at = now()
			WHERE id = $1 AND status = ANY($4)`, id, string(to), why, statusStrings(from))
		if err != nil {
			return fmt.Errorf("Repo - Transition - update: %w", err)
		}

		if o, err = orderBy(ctx, tx, `o.id = $1`, id); err != nil {
			return fmt.Errorf("Repo - Transition - select: %w", err)
		}

		if moved = tag.RowsAffected() == 1; !moved || out == nil {
			return nil
		}

		return insertOutbox(ctx, tx, out(o))
	})

	return o, moved, err
}

// DuePending -: oldest first.
func (r *Repo) DuePending(ctx context.Context, createdBefore time.Time, limit int) ([]entity.Order, error) {
	return r.orders(ctx, "Repo - DuePending", `o.status = 'PENDING' AND o.created_at < $1
		ORDER BY o.created_at, o.id LIMIT $2`, createdBefore, limit)
}

// PaidBetween -.
func (r *Repo) PaidBetween(ctx context.Context, from, to time.Time) ([]entity.Order, error) {
	return r.orders(ctx, "Repo - PaidBetween", `o.paid_at >= $1 AND o.paid_at < $2 ORDER BY o.paid_at`, from, to)
}

// CountNeedsReview -.
func (r *Repo) CountNeedsReview(ctx context.Context) (int, error) {
	var n int
	if err := r.Pool.QueryRow(ctx, `SELECT count(*) FROM checkout_orders WHERE needs_review`).Scan(&n); err != nil {
		return 0, fmt.Errorf("Repo - CountNeedsReview: %w", err)
	}

	return n, nil
}

// WithLockedOrder -.
func (r *Repo) WithLockedOrder(ctx context.Context, invoice string, fn func(tx repo.PaymentTx, o entity.Order) error) error {
	return r.inTx(ctx, "Repo - WithLockedOrder", func(tx pgx.Tx) error {
		o, err := orderBy(ctx, tx, `o.invoice_number = $1 FOR UPDATE OF o`, invoice)
		if err != nil {
			return fmt.Errorf("Repo - WithLockedOrder: %w", err)
		}

		return fn(paymentTx{tx: tx}, o)
	})
}

// nullable maps "" (a top-up's channel, tier and sub action) to NULL.
func nullable(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}
