package persistent

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

const (
	_payoutCols = `p.id, p.number, p.channel_id, p.amount_vnd, p.bank_code, p.account_last4, p.bank_info_encrypted,
		p.status, p.reject_reason, p.reviewed_by, p.reviewed_at, p.created_at`
	_openPayoutKey = "payout_requests_one_open_idx"
)

// Totals -.
func (r *Repo) Totals(ctx context.Context, channelID string, from, to time.Time) (entity.Totals, error) {
	var t entity.Totals

	err := r.Pool.QueryRow(ctx, `SELECT
			COALESCE(sum(CASE WHEN kind = 'SUB_REVENUE' THEN
				CASE WHEN direction = 'CREDIT' THEN amount_vnd ELSE -amount_vnd END END), 0),
			COALESCE(sum(CASE WHEN kind = 'PLATFORM_FEE' THEN
				CASE WHEN direction = 'DEBIT' THEN amount_vnd ELSE -amount_vnd END END), 0)
		FROM ledger_entries WHERE channel_id = $1 AND created_at >= $2 AND created_at <= $3`,
		channelID, from, to).Scan(&t.Gross, &t.PlatformFee)
	if err != nil {
		return t, fmt.Errorf("Repo - Totals: %w", err)
	}

	return t, nil
}

// Balance -.
func (r *Repo) Balance(ctx context.Context, channelID string) (bal, pending int64, err error) {
	return balance(ctx, r.Pool, channelID)
}

func balance(ctx context.Context, q querier, channelID string) (bal, pending int64, err error) {
	err = q.QueryRow(ctx, `SELECT
			(SELECT COALESCE(sum(CASE WHEN direction = 'CREDIT' THEN amount_vnd ELSE -amount_vnd END), 0)
				FROM ledger_entries WHERE channel_id = $1),
			(SELECT COALESCE(sum(amount_vnd), 0) FROM payout_requests WHERE channel_id = $1 AND status = 'REQUESTED')`,
		channelID).Scan(&bal, &pending)
	if err != nil {
		return 0, 0, fmt.Errorf("Repo - Balance: %w", err)
	}

	return bal, pending, nil
}

// Entries -: newest first, with the source transaction / payout.
func (r *Repo) Entries(ctx context.Context, channelID string, after *repo.Cursor, limit int) ([]entity.LedgerEntry, error) {
	set, at, id := pageArgs(after)

	rows, err := r.Pool.Query(ctx, `SELECT l.id, l.channel_id, l.transaction_id, l.payout_request_id, l.direction, l.kind,
			l.amount_vnd, l.created_at, COALESCE(t.type, 'PAYOUT'), o.invoice_number, p.number
		FROM ledger_entries l
		LEFT JOIN transactions t ON t.id = l.transaction_id
		LEFT JOIN checkout_orders o ON o.id = t.order_id
		LEFT JOIN payout_requests p ON p.id = l.payout_request_id
		WHERE l.channel_id = $1 AND (NOT $2 OR (l.created_at, l.id) < ($3, $4::uuid))
		ORDER BY l.created_at DESC, l.id DESC LIMIT $5`, channelID, set, at, id, limit)
	if err != nil {
		return nil, fmt.Errorf("Repo - Entries: %w", err)
	}

	out, err := collect(rows, func(s scanner) (entity.LedgerEntry, error) {
		var e entity.LedgerEntry

		err := s.Scan(&e.ID, &e.ChannelID, &e.TransactionID, &e.PayoutID, &e.Direction, &e.Kind, &e.Amount, &e.CreatedAt,
			&e.Source, &e.InvoiceNumber, &e.PayoutNumber)

		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("Repo - Entries: %w", err)
	}

	return out, nil
}

func scanPayout(s scanner) (entity.Payout, error) {
	var p entity.Payout

	err := s.Scan(&p.ID, &p.Number, &p.ChannelID, &p.Amount, &p.BankCode, &p.AccountLast4, &p.BankEncrypted, &p.Status,
		&p.RejectReason, &p.ReviewedBy, &p.ReviewedAt, &p.CreatedAt)

	return p, err
}

func payoutBy(ctx context.Context, q querier, where string, args ...any) (entity.Payout, error) {
	p, err := scanPayout(q.QueryRow(ctx, `SELECT `+_payoutCols+` FROM payout_requests p WHERE `+where, args...))
	if err != nil {
		return p, fmt.Errorf("select payout: %w", notFound(err))
	}

	return p, nil
}

// CreatePayout -. The per-channel advisory lock and payout_requests_one_open_idx keep one open request.
func (r *Repo) CreatePayout(ctx context.Context, p entity.Payout, out func(entity.Payout) []repo.Outbox) (entity.Payout, error) {
	err := r.inTx(ctx, "Repo - CreatePayout", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('billing:payout:' || $1, 0))`,
			p.ChannelID); err != nil {
			return fmt.Errorf("Repo - CreatePayout - lock: %w", err)
		}

		if err := payoutAllowed(ctx, tx, p); err != nil {
			return err
		}

		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO payout_requests (channel_id, amount_vnd, bank_info_encrypted, bank_code,
			account_last4) VALUES ($1, $2, $3, $4, $5) RETURNING id`, p.ChannelID, p.Amount, p.BankEncrypted, p.BankCode,
			p.AccountLast4).Scan(&id); err != nil {
			if isUnique(err, _openPayoutKey) {
				return entity.StateError(entity.ReasonPayoutPending)
			}

			return fmt.Errorf("Repo - CreatePayout - insert: %w", err)
		}

		created, err := payoutBy(ctx, tx, `p.id = $1`, id)
		if err != nil {
			return err
		}

		p = created

		return insertOutbox(ctx, tx, out(p))
	})

	return p, err
}

// payoutAllowed: one open request per channel (BR-H33), amount within the available balance.
func payoutAllowed(ctx context.Context, tx pgx.Tx, p entity.Payout) error {
	bal, pending, err := balance(ctx, tx, p.ChannelID)
	if err != nil {
		return err
	}

	var open bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payout_requests WHERE channel_id = $1
		AND status = 'REQUESTED')`, p.ChannelID).Scan(&open); err != nil {
		return fmt.Errorf("Repo - CreatePayout - open: %w", err)
	}

	if open {
		return entity.StateError(entity.ReasonPayoutPending)
	}

	if available := bal - pending; available < p.Amount {
		return entity.StateError(entity.ReasonInsufficientBalance, "available", strconv.FormatInt(available, 10))
	}

	return nil
}

func (r *Repo) payouts(ctx context.Context, op, where string, args ...any) ([]entity.Payout, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_payoutCols+` FROM payout_requests p WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	out, err := collect(rows, scanPayout)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	return out, nil
}

// ForChannel -: newest first.
func (r *Repo) ForChannel(ctx context.Context, channelID string, after *repo.Cursor, limit int) ([]entity.Payout, error) {
	set, at, id := pageArgs(after)

	return r.payouts(ctx, "Repo - ForChannel", `p.channel_id = $1 AND (NOT $2 OR (p.created_at, p.id) < ($3, $4::uuid))
		ORDER BY p.created_at DESC, p.id DESC LIMIT $5`, channelID, set, at, id, limit)
}

// ListPayouts -: REQUESTED oldest first (a queue), the others newest first.
func (r *Repo) ListPayouts(ctx context.Context, status *entity.PayoutStatus, after *repo.Cursor, limit int,
) ([]entity.Payout, error) {
	set, at, id := pageArgs(after)

	if status != nil && *status == entity.PayoutRequested {
		return r.payouts(ctx, "Repo - ListPayouts", `p.status = 'REQUESTED'
			AND (NOT $1 OR (p.created_at, p.id) > ($2, $3::uuid)) ORDER BY p.created_at, p.id LIMIT $4`, set, at, id, limit)
	}

	var st *string

	if status != nil {
		s := string(*status)
		st = &s
	}

	return r.payouts(ctx, "Repo - ListPayouts", `($1::text IS NULL OR p.status = $1)
		AND (NOT $2 OR (p.created_at, p.id) < ($3, $4::uuid)) ORDER BY p.created_at DESC, p.id DESC LIMIT $5`,
		st, set, at, id, limit)
}

// Payout -.
func (r *Repo) Payout(ctx context.Context, id string) (entity.Payout, error) {
	p, err := payoutBy(ctx, r.Pool, `p.id = $1`, id)
	if err != nil {
		return p, fmt.Errorf("Repo - Payout: %w", err)
	}

	return p, nil
}

// Review -.
func (r *Repo) Review(ctx context.Context, id, adminID string, to entity.PayoutStatus, reason *string, now time.Time,
	out func(entity.Payout) []repo.Outbox,
) (entity.Payout, error) {
	var p entity.Payout

	err := r.inTx(ctx, "Repo - Review", func(tx pgx.Tx) error {
		cur, err := payoutBy(ctx, tx, `p.id = $1 FOR UPDATE`, id)
		if err != nil {
			return fmt.Errorf("Repo - Review: %w", err)
		}

		if cur.Status != entity.PayoutRequested {
			return entity.ErrInvalidState
		}

		if _, err := tx.Exec(ctx, `UPDATE payout_requests SET status = $2, reviewed_by = $3, reviewed_at = $4,
			reject_reason = $5 WHERE id = $1`, id, string(to), adminID, now, reason); err != nil {
			return fmt.Errorf("Repo - Review - update: %w", err)
		}

		if to == entity.PayoutApproved {
			if err := insertLedger(ctx, tx, []entity.LedgerEntry{{
				ChannelID: cur.ChannelID, PayoutID: &cur.ID, Direction: entity.Debit, Kind: entity.LedgerPayout, Amount: cur.Amount,
			}}); err != nil {
				return fmt.Errorf("Repo - Review: %w", err)
			}
		}

		created, err := payoutBy(ctx, tx, `p.id = $1`, id)
		if err != nil {
			return err
		}

		p = created

		return insertOutbox(ctx, tx, out(p))
	})

	return p, err
}

// CountRequested -.
func (r *Repo) CountRequested(ctx context.Context) (int, error) {
	var n int
	if err := r.Pool.QueryRow(ctx, `SELECT count(*) FROM payout_requests WHERE status = 'REQUESTED'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("Repo - CountRequested: %w", err)
	}

	return n, nil
}
