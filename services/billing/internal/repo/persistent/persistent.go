// Package persistent implements billing storage on PostgreSQL (database nxl_billing).
package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
)

const (
	_consumer       = "billing-svc"
	_uniqueViolated = "23505"
	_liveStatuses   = `('ACTIVE', 'CANCELLED_PENDING_END')`
)

// Repo implements the storage contracts of billing-svc.
type Repo struct {
	*postgres.Postgres
}

var (
	_ repo.ChannelRepo     = (*Repo)(nil)
	_ repo.TierRepo        = (*Repo)(nil)
	_ repo.OrderRepo       = (*Repo)(nil)
	_ repo.SubRepo         = (*Repo)(nil)
	_ repo.LedgerRepo      = (*Repo)(nil)
	_ repo.PayoutRepo      = (*Repo)(nil)
	_ repo.WalletRepo      = (*Repo)(nil)
	_ repo.GiftRepo        = (*Repo)(nil)
	_ repo.AuditRepo       = (*Repo)(nil)
	_ repo.TransactionRepo = (*Repo)(nil)
	_ repo.IdempotencyRepo = (*Repo)(nil)
)

// New -.
func New(pg *postgres.Postgres) *Repo { return &Repo{Postgres: pg} }

type scanner interface {
	Scan(dest ...any) error
}

// querier is satisfied by the pool and by a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (r *Repo) inTx(ctx context.Context, op string, fn func(tx pgx.Tx) error) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s - begin: %w", op, err)
	}

	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s - commit: %w", op, err)
	}

	return nil
}

// inEventTx runs fn once per event id: a redelivered event finds its processed_events row and is skipped.
// An empty id (backfill) always runs.
func (r *Repo) inEventTx(ctx context.Context, op, eventID string, fn func(tx pgx.Tx) error) error {
	return r.inTx(ctx, op, func(tx pgx.Tx) error {
		if eventID != "" {
			tag, err := tx.Exec(ctx,
				`INSERT INTO processed_events (event_id, consumer) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				eventID, _consumer)
			if err != nil {
				return fmt.Errorf("%s - processed_events: %w", op, err)
			}

			if tag.RowsAffected() == 0 {
				return nil
			}
		}

		return fn(tx)
	})
}

func insertOutbox(ctx context.Context, q querier, rows []repo.Outbox) error {
	for _, o := range rows {
		b, err := json.Marshal(o.Payload)
		if err != nil {
			return fmt.Errorf("outbox marshal: %w", err)
		}

		if _, err := q.Exec(ctx,
			`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES ($1, $2, $3, $4)`,
			o.AggregateType, o.AggregateID, o.EventType, b); err != nil {
			return fmt.Errorf("outbox insert: %w", err)
		}
	}

	return nil
}

func isUnique(err error, constraint string) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == _uniqueViolated && (constraint == "" || pgErr.ConstraintName == constraint)
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.ErrNotFound
	}

	return err
}

// Record writes audit-only event rows.
func (r *Repo) Record(ctx context.Context, rows []repo.Outbox) error {
	return r.inTx(ctx, "Repo - Record", func(tx pgx.Tx) error {
		return insertOutbox(ctx, tx, rows)
	})
}

// collect scans every row with fn.
func collect[T any](rows pgx.Rows, fn func(scanner) (T, error)) ([]T, error) {
	defer rows.Close()

	var out []T

	for rows.Next() {
		v, err := fn(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	return out, nil
}
