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
		p.status, p.reject_reason, p.reviewed_by, p.reviewed_at, p.created_at, p.provider, p.provider_ref, p.paid_at,
		p.settled_by, p.settled_at, p.failure_reason`
	_openPayoutKey = "payout_requests_one_open_idx"
)

// Totals -.
func (r *Repo) Totals(ctx context.Context, channelID string, from, to time.Time) (entity.Totals, error) {
	var t entity.Totals

	err := r.Pool.QueryRow(ctx, `SELECT
			COALESCE(sum(CASE WHEN kind IN ('SUB_REVENUE', 'GIFT_REVENUE') THEN
				CASE WHEN direction = 'CREDIT' THEN amount_vnd ELSE -amount_vnd END END), 0),
			COALESCE(sum(CASE WHEN kind = 'PLATFORM_FEE' THEN
				CASE WHEN direction = 'DEBIT' THEN amount_vnd ELSE -amount_vnd END END), 0),
			COALESCE(sum(CASE WHEN kind = 'GIFT_REVENUE' THEN
				CASE WHEN direction = 'CREDIT' THEN amount_vnd ELSE -amount_vnd END END), 0)
		FROM ledger_entries WHERE channel_id = $1 AND created_at >= $2 AND created_at <= $3`,
		channelID, from, to).Scan(&t.Gross, &t.PlatformFee, &t.Gifts)
	if err != nil {
		return t, fmt.Errorf("Repo - Totals: %w", err)
	}

	return t, nil
}

// Balance -.
func (r *Repo) Balance(ctx context.Context, channelID string) (available, held int64, err error) {
	err = r.Pool.QueryRow(ctx, `SELECT
			(SELECT COALESCE(sum(CASE WHEN direction = 'CREDIT' THEN amount_vnd ELSE -amount_vnd END), 0)
				FROM ledger_entries WHERE channel_id = $1),
			(SELECT COALESCE(sum(amount_vnd), 0) FROM payout_requests
				WHERE channel_id = $1 AND status IN ('REQUESTED', 'APPROVED', 'PROCESSING'))`,
		channelID).Scan(&available, &held)
	if err != nil {
		return 0, 0, fmt.Errorf("Repo - Balance: %w", err)
	}

	return available, held, nil
}

// Entries -: newest first, with the source transaction / payout.
func (r *Repo) Entries(ctx context.Context, channelID string, after *repo.Cursor, limit int) ([]entity.LedgerEntry, error) {
	set, at, id := pageArgs(after)

	rows, err := r.Pool.Query(ctx, `SELECT l.id, l.channel_id, l.transaction_id, l.payout_request_id, l.direction, l.kind,
			l.amount_vnd, l.created_at,
			CASE WHEN l.gift_send_id IS NOT NULL THEN 'GIFT' ELSE COALESCE(t.type, 'PAYOUT') END,
			o.invoice_number, p.number
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
		&p.RejectReason, &p.ReviewedBy, &p.ReviewedAt, &p.CreatedAt, &p.Provider, &p.ProviderRef, &p.PaidAt, &p.SettledBy,
		&p.SettledAt, &p.FailureReason)

	return p, err
}

func payoutBy(ctx context.Context, q querier, where string, args ...any) (entity.Payout, error) {
	p, err := scanPayout(q.QueryRow(ctx, `SELECT `+_payoutCols+` FROM payout_requests p WHERE `+where, args...))
	if err != nil {
		return p, fmt.Errorf("select payout: %w", notFound(err))
	}

	return p, nil
}

// CreatePayout -. The per-channel advisory lock and payout_requests_one_open_idx keep one open request; the
// amount leaves the available balance at once (DEBIT PAYOUT_HOLD, BR-H37).
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

		if err := insertLedger(ctx, tx, []entity.LedgerEntry{{
			ChannelID: p.ChannelID, PayoutID: &id, Direction: entity.Debit, Kind: entity.LedgerPayoutHold, Amount: p.Amount,
		}}); err != nil {
			return fmt.Errorf("Repo - CreatePayout: %w", err)
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

// payoutAllowed: one open request per channel (BR-H39), amount within the available balance.
func payoutAllowed(ctx context.Context, tx pgx.Tx, p entity.Payout) error {
	var (
		available int64
		open      bool
	)

	if err := tx.QueryRow(ctx, `SELECT
			(SELECT COALESCE(sum(CASE WHEN direction = 'CREDIT' THEN amount_vnd ELSE -amount_vnd END), 0)
				FROM ledger_entries WHERE channel_id = $1),
			EXISTS (SELECT 1 FROM payout_requests WHERE channel_id = $1
				AND status IN ('REQUESTED', 'APPROVED', 'PROCESSING'))`, p.ChannelID).Scan(&available, &open); err != nil {
		return fmt.Errorf("Repo - CreatePayout - balance: %w", err)
	}

	if open {
		return entity.StateError(entity.ReasonPayoutPending)
	}

	if available < p.Amount {
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

// Move -. reviewed_* are set when the payout leaves REQUESTED, settled_* when it reaches PAID or FAILED.
func (r *Repo) Move(ctx context.Context, id string, m repo.PayoutMove, out func(entity.Payout) []repo.Outbox,
) (entity.Payout, error) {
	var p entity.Payout

	err := r.inTx(ctx, "Repo - Move", func(tx pgx.Tx) error {
		cur, err := payoutBy(ctx, tx, `p.id = $1 FOR UPDATE`, id)
		if err != nil {
			return fmt.Errorf("Repo - Move: %w", err)
		}

		if !cur.CanMoveTo(m.To) {
			return entity.StateError(entity.ReasonPayoutState, "status", string(cur.Status))
		}

		if _, err := tx.Exec(ctx, `UPDATE payout_requests SET status = $2::varchar,
				reviewed_by = CASE WHEN status = 'REQUESTED' THEN $3::uuid ELSE reviewed_by END,
				reviewed_at = CASE WHEN status = 'REQUESTED' THEN $4::timestamptz ELSE reviewed_at END,
				reject_reason = CASE WHEN $2 = 'REJECTED' THEN $5::text ELSE reject_reason END,
				failure_reason = CASE WHEN $2 = 'FAILED' THEN $5 ELSE failure_reason END,
				provider = COALESCE($6::varchar, provider),
				provider_ref = COALESCE($7::varchar, provider_ref),
				paid_at = CASE WHEN $2 = 'PAID' THEN $4 ELSE paid_at END,
				settled_by = CASE WHEN $2 IN ('PAID', 'FAILED') THEN $3 ELSE settled_by END,
				settled_at = CASE WHEN $2 IN ('PAID', 'FAILED') THEN $4 ELSE settled_at END
			WHERE id = $1`, id, string(m.To), m.ActorID, m.At, m.Reason, m.Provider, m.Ref); err != nil {
			return fmt.Errorf("Repo - Move - update: %w", err)
		}

		if m.To.Reversed() {
			if err := insertLedger(ctx, tx, []entity.LedgerEntry{{
				ChannelID: cur.ChannelID, PayoutID: &cur.ID, Direction: entity.Credit, Kind: entity.LedgerPayoutReversal,
				Amount: cur.Amount,
			}}); err != nil {
				return fmt.Errorf("Repo - Move: %w", err)
			}
		}

		moved, err := payoutBy(ctx, tx, `p.id = $1`, id)
		if err != nil {
			return err
		}

		p = moved

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
