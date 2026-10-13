// Package outbox publishes rows of a service's `outbox` table to the event bus (transactional outbox).
//
// A use case writes its business change and the outbox row in one transaction; this worker then
// publishes unpublished rows in order and marks them. A crash between publish and mark re-publishes
// the row with the same id, which JetStream de-duplicates (Nats-Msg-Id) and consumers ignore
// (processed_events).
package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minhnn/nexuslive-be/pkg/logger"
)

const (
	_defaultInterval = 200 * time.Millisecond
	_defaultBatch    = 100
)

// Publisher is satisfied by *eventbus.Bus.
type Publisher interface {
	// PublishAt publishes with the row's created_at as the event's occurrence time.
	PublishAt(ctx context.Context, eventType, id string, occurredAt time.Time, payload []byte) error
}

// Worker -.
type Worker struct {
	pool     *pgxpool.Pool
	pub      Publisher
	l        logger.Interface
	interval time.Duration
	batch    int
}

// New -.
func New(pool *pgxpool.Pool, pub Publisher, l logger.Interface) *Worker {
	return &Worker{pool: pool, pub: pub, l: l, interval: _defaultInterval, batch: _defaultBatch}
}

// Run polls until ctx is done.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for {
				n, err := w.PublishBatch(ctx)
				if err != nil {
					w.l.Warn("outbox - publish: %v", err)

					break
				}

				if n < w.batch {
					break
				}
			}
		}
	}
}

// PublishBatch publishes up to one batch in created_at order and returns how many rows it published.
// FOR UPDATE SKIP LOCKED lets several replicas share the table without publishing a row twice.
func (w *Worker) PublishBatch(ctx context.Context) (int, error) {
	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("outbox - begin: %w", err)
	}

	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	batch, err := w.lockBatch(ctx, tx)
	if err != nil {
		return 0, err
	}

	published, pubErr := w.publish(ctx, batch)

	if len(published) > 0 {
		if _, uerr := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1::uuid[])`, published); uerr != nil {
			return 0, fmt.Errorf("outbox - mark published: %w", uerr)
		}

		if cerr := tx.Commit(ctx); cerr != nil {
			return 0, fmt.Errorf("outbox - commit: %w", cerr)
		}
	}

	if pubErr != nil {
		return len(published), fmt.Errorf("outbox - publish: %w", pubErr)
	}

	return len(published), nil
}

type outboxRow struct {
	id, typ   string
	createdAt time.Time
	payload   []byte
}

func (w *Worker) lockBatch(ctx context.Context, tx pgx.Tx) ([]outboxRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT id::text, event_type, created_at, payload FROM outbox
		 WHERE published_at IS NULL
		 ORDER BY created_at, id
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`, w.batch)
	if err != nil {
		return nil, fmt.Errorf("outbox - select: %w", err)
	}
	defer rows.Close()

	var batch []outboxRow

	for rows.Next() {
		var r outboxRow
		if err = rows.Scan(&r.id, &r.typ, &r.createdAt, &r.payload); err != nil {
			return nil, fmt.Errorf("outbox - scan: %w", err)
		}

		batch = append(batch, r)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox - rows: %w", err)
	}

	return batch, nil
}

// publish stops at the first failure so events keep their order; it returns the ids that went out.
func (w *Worker) publish(ctx context.Context, batch []outboxRow) ([]string, error) {
	published := make([]string, 0, len(batch))

	for _, r := range batch {
		if err := w.pub.PublishAt(ctx, r.typ, r.id, r.createdAt, r.payload); err != nil {
			return published, err
		}

		published = append(published, r.id)
	}

	return published, nil
}
