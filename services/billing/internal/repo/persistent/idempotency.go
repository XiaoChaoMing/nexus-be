package persistent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

const _purgeBatch = 1000

// Begin -: INSERT … ON CONFLICT DO NOTHING; an unfinished key older than stale is taken over.
func (r *Repo) Begin(ctx context.Context, k repo.IdemKey, hash []byte, stale time.Duration) (repo.IdemState, error) {
	tag, err := r.Pool.Exec(ctx, `INSERT INTO idempotency_keys (user_id, method, path, key, request_hash)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, k.UserID, k.Method, k.Path, k.Key, hash)
	if err != nil {
		return repo.IdemState{}, fmt.Errorf("Repo - Begin - insert: %w", err)
	}

	if tag.RowsAffected() == 1 {
		return repo.IdemState{Outcome: repo.IdemNew}, nil
	}

	var (
		stored  []byte
		status  *int
		body    []byte
		created time.Time
		now     time.Time
	)

	err = r.Pool.QueryRow(ctx, `SELECT request_hash, status_code, response, created_at, now() FROM idempotency_keys
		WHERE user_id = $1 AND method = $2 AND path = $3 AND key = $4`, k.UserID, k.Method, k.Path, k.Key).
		Scan(&stored, &status, &body, &created, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.Begin(ctx, k, hash, stale) // purged or aborted in between
	}

	if err != nil {
		return repo.IdemState{}, fmt.Errorf("Repo - Begin - select: %w", err)
	}

	switch {
	case !bytes.Equal(stored, hash):
		return repo.IdemState{Outcome: repo.IdemMismatch}, nil
	case status != nil:
		return repo.IdemState{Outcome: repo.IdemDone, Status: *status, Body: body}, nil
	case created.After(now.Add(-stale)):
		return repo.IdemState{Outcome: repo.IdemInProgress}, nil
	}

	tag, err = r.Pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now()
		WHERE user_id = $1 AND method = $2 AND path = $3 AND key = $4 AND status_code IS NULL AND created_at = $5`,
		k.UserID, k.Method, k.Path, k.Key, created)
	if err != nil {
		return repo.IdemState{}, fmt.Errorf("Repo - Begin - take over: %w", err)
	}

	if tag.RowsAffected() == 1 {
		return repo.IdemState{Outcome: repo.IdemNew}, nil
	}

	return repo.IdemState{Outcome: repo.IdemInProgress}, nil
}

// Complete -.
func (r *Repo) Complete(ctx context.Context, k repo.IdemKey, status int, body []byte) error {
	if _, err := r.Pool.Exec(ctx, `UPDATE idempotency_keys SET status_code = $5, response = $6, completed_at = now()
		WHERE user_id = $1 AND method = $2 AND path = $3 AND key = $4`, k.UserID, k.Method, k.Path, k.Key, status,
		body); err != nil {
		return fmt.Errorf("Repo - Complete: %w", err)
	}

	return nil
}

// Abort -.
func (r *Repo) Abort(ctx context.Context, k repo.IdemKey) error {
	if _, err := r.Pool.Exec(ctx, `DELETE FROM idempotency_keys
		WHERE user_id = $1 AND method = $2 AND path = $3 AND key = $4 AND status_code IS NULL`,
		k.UserID, k.Method, k.Path, k.Key); err != nil {
		return fmt.Errorf("Repo - Abort: %w", err)
	}

	return nil
}

// Purge -: in batches so a large backlog does not hold one long lock.
func (r *Repo) Purge(ctx context.Context, before time.Time) (int64, error) {
	return r.purge(ctx, "Repo - Purge", `DELETE FROM idempotency_keys WHERE ctid IN
		(SELECT ctid FROM idempotency_keys WHERE created_at < $1 LIMIT $2)`, before)
}

// PurgeProcessed -.
func (r *Repo) PurgeProcessed(ctx context.Context, before time.Time) (int64, error) {
	return r.purge(ctx, "Repo - PurgeProcessed", `DELETE FROM processed_events WHERE ctid IN
		(SELECT ctid FROM processed_events WHERE processed_at < $1 LIMIT $2)`, before)
}

func (r *Repo) purge(ctx context.Context, op, sql string, before time.Time) (int64, error) {
	var total int64

	for {
		tag, err := r.Pool.Exec(ctx, sql, before, _purgeBatch)
		if err != nil {
			return total, fmt.Errorf("%s: %w", op, err)
		}

		total += tag.RowsAffected()
		if tag.RowsAffected() < _purgeBatch {
			return total, nil
		}
	}
}

// Transaction -.
func (r *Repo) Transaction(ctx context.Context, id string) (entity.Transaction, error) {
	return r.txnBy(ctx, "Repo - Transaction", `id = $1`, id)
}

// ChargeOf -.
func (r *Repo) ChargeOf(ctx context.Context, orderID string) (entity.Transaction, error) {
	return r.txnBy(ctx, "Repo - ChargeOf", `order_id = $1 AND type = 'CHARGE' ORDER BY created_at LIMIT 1`, orderID)
}

func (r *Repo) txnBy(ctx context.Context, op, where string, args ...any) (entity.Transaction, error) {
	var t entity.Transaction

	err := r.Pool.QueryRow(ctx, `SELECT id, order_id, type, amount_vnd, psp_ref, source, raw_webhook FROM transactions
		WHERE `+where, args...).Scan(&t.ID, &t.OrderID, &t.Type, &t.Amount, &t.PSPRef, &t.Source, &t.Raw)
	if err != nil {
		return t, fmt.Errorf("%s: %w", op, notFound(err))
	}

	return t, nil
}
