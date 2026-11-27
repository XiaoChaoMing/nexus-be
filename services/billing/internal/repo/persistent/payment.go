package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

// paymentTx implements repo.PaymentTx on the transaction of WithLockedOrder.
type paymentTx struct {
	tx pgx.Tx
}

var _ repo.PaymentTx = paymentTx{}

// InsertTransaction -. The unique (psp_ref, type) makes a redelivered notification a no-op.
func (p paymentTx) InsertTransaction(ctx context.Context, t entity.Transaction) (id string, inserted bool, err error) {
	raw := t.Raw
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}

	err = p.tx.QueryRow(ctx, `INSERT INTO transactions (order_id, type, amount_vnd, psp_ref, raw_webhook, source)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (psp_ref, type) DO NOTHING RETURNING id`,
		t.OrderID, string(t.Type), t.Amount, t.PSPRef, raw, t.Source).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("PaymentTx - InsertTransaction: %w", err)
	}

	return id, true, nil
}

// MarkPaid -: allowed from every payable status (BR-H20), never after AMOUNT_MISMATCH.
func (p paymentTx) MarkPaid(ctx context.Context, orderID string, m repo.MarkPaid) (bool, error) {
	var method *string

	if m.Method != nil {
		s := string(*m.Method)
		method = &s
	}

	tag, err := p.tx.Exec(ctx, `UPDATE checkout_orders SET status = 'PAID', paid_at = $2, psp_ref = COALESCE($3, psp_ref),
			payment_method = COALESCE(payment_method, $4), needs_review = $5, subscription_id = $6, failure_reason = NULL,
			updated_at = now()
		WHERE id = $1 AND status = ANY($7) AND failure_reason IS DISTINCT FROM 'AMOUNT_MISMATCH'`,
		orderID, m.PaidAt, m.ProviderOrderID, method, m.NeedsReview, m.SubscriptionID,
		statusStrings(entity.PayableStatuses()))
	if err != nil {
		return false, fmt.Errorf("PaymentTx - MarkPaid: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

// MarkVoided -.
func (p paymentTx) MarkVoided(ctx context.Context, orderID string) (bool, error) {
	tag, err := p.tx.Exec(ctx, `UPDATE checkout_orders SET status = 'VOIDED', updated_at = now()
		WHERE id = $1 AND status = 'PAID'`, orderID)
	if err != nil {
		return false, fmt.Errorf("PaymentTx - MarkVoided: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

// MarkMismatch -.
func (p paymentTx) MarkMismatch(ctx context.Context, orderID string) (bool, error) {
	tag, err := p.tx.Exec(ctx, `UPDATE checkout_orders SET status = 'FAILED', failure_reason = 'AMOUNT_MISMATCH',
			updated_at = now()
		WHERE id = $1 AND status = ANY($2)`, orderID, statusStrings(entity.PayableStatuses()))
	if err != nil {
		return false, fmt.Errorf("PaymentTx - MarkMismatch: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

func lockSub(ctx context.Context, q querier, where string, args ...any) (*entity.Subscription, error) {
	s, err := scanSub(q.QueryRow(ctx, `SELECT `+_subCols+_subFrom+` WHERE `+where+` FOR UPDATE OF s`, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("PaymentTx - lock sub: %w", err)
	}

	return &s, nil
}

// LiveSub -.
func (p paymentTx) LiveSub(ctx context.Context, userID, channelID string) (*entity.Subscription, error) {
	return lockSub(ctx, p.tx, `s.user_id = $1 AND s.channel_id = $2 AND s.status IN `+_liveStatuses, userID, channelID)
}

// Sub -.
func (p paymentTx) Sub(ctx context.Context, id string) (*entity.Subscription, error) {
	return lockSub(ctx, p.tx, `s.id = $1`, id)
}

// InsertSub -.
func (p paymentTx) InsertSub(ctx context.Context, s entity.Subscription) (entity.Subscription, error) {
	var id string

	if err := p.tx.QueryRow(ctx, `INSERT INTO subscriptions (user_id, channel_id, tier_id, status, current_period_start,
			current_period_end, auto_renew) VALUES ($1, $2, $3, 'ACTIVE', $4, $5, true) RETURNING id`,
		s.UserID, s.ChannelID, s.TierID, s.CurrentPeriodStart, s.CurrentPeriodEnd).Scan(&id); err != nil {
		return s, fmt.Errorf("PaymentTx - InsertSub: %w", err)
	}

	return subBy(ctx, p.tx, `s.id = $1`, id)
}

// ExtendSub -.
func (p paymentTx) ExtendSub(ctx context.Context, id string, end time.Time) (entity.Subscription, error) {
	if _, err := p.tx.Exec(ctx, `UPDATE subscriptions SET status = 'ACTIVE', current_period_end = $2, auto_renew = true,
		cancelled_at = NULL, updated_at = now() WHERE id = $1`, id, end); err != nil {
		return entity.Subscription{}, fmt.Errorf("PaymentTx - ExtendSub: %w", err)
	}

	return subBy(ctx, p.tx, `s.id = $1`, id)
}

// ReduceSub -.
func (p paymentTx) ReduceSub(ctx context.Context, id string, end time.Time) error {
	if _, err := p.tx.Exec(ctx, `UPDATE subscriptions SET current_period_end = $2, updated_at = now() WHERE id = $1`,
		id, end); err != nil {
		return fmt.Errorf("PaymentTx - ReduceSub: %w", err)
	}

	return nil
}

// ExpireSub -.
func (p paymentTx) ExpireSub(ctx context.Context, id string, end time.Time, reason string) error {
	if _, err := p.tx.Exec(ctx, `UPDATE subscriptions SET status = 'EXPIRED', expired_reason = $3, current_period_end = $2,
		updated_at = now() WHERE id = $1`, id, end, reason); err != nil {
		return fmt.Errorf("PaymentTx - ExpireSub: %w", err)
	}

	return nil
}

// ChargeFee -.
func (p paymentTx) ChargeFee(ctx context.Context, orderID string) (int64, error) {
	var fee int64
	if err := p.tx.QueryRow(ctx, `SELECT COALESCE(sum(l.amount_vnd), 0) FROM ledger_entries l
		JOIN transactions t ON t.id = l.transaction_id
		WHERE t.order_id = $1 AND t.type = 'CHARGE' AND l.kind = 'PLATFORM_FEE' AND l.direction = 'DEBIT'`,
		orderID).Scan(&fee); err != nil {
		return 0, fmt.Errorf("PaymentTx - ChargeFee: %w", err)
	}

	return fee, nil
}

// InsertLedger -.
func (p paymentTx) InsertLedger(ctx context.Context, entries []entity.LedgerEntry) error {
	return insertLedger(ctx, p.tx, entries)
}

func insertLedger(ctx context.Context, q querier, entries []entity.LedgerEntry) error {
	for _, e := range entries {
		if _, err := q.Exec(ctx, `INSERT INTO ledger_entries (channel_id, transaction_id, payout_request_id, gift_send_id,
			direction, kind, amount_vnd) VALUES ($1, $2, $3, $4, $5, $6, $7)`, e.ChannelID, e.TransactionID, e.PayoutID,
			e.GiftSendID, string(e.Direction), string(e.Kind), e.Amount); err != nil {
			return fmt.Errorf("insert ledger: %w", err)
		}
	}

	return nil
}

// InsertWallet -.
func (p paymentTx) InsertWallet(ctx context.Context, entries []entity.WalletEntry) error {
	return insertWallet(ctx, p.tx, entries)
}

// InsertOutbox -.
func (p paymentTx) InsertOutbox(ctx context.Context, rows []repo.Outbox) error {
	return insertOutbox(ctx, p.tx, rows)
}
