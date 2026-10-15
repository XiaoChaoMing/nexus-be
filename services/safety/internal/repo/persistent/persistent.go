// Package persistent implements safety-svc storage on PostgreSQL (database nxl_safety).
package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
)

const (
	_consumer = "safety-svc"
	// _source is audit_logs.source_service of rows safety writes itself.
	_source = "safety-svc"
	// _open is the SQL list of statuses an admin still has to handle.
	_open = `('OPEN', 'IN_REVIEW')`
)

// Repo implements the report, action and replica contracts of safety-svc; Audit the audit log.
type Repo struct {
	*postgres.Postgres
}

var (
	_ repo.ReportRepo  = (*Repo)(nil)
	_ repo.ActionRepo  = (*Repo)(nil)
	_ repo.AuditRepo   = Audit{}
	_ repo.ReplicaRepo = (*Repo)(nil)
)

// New -.
func New(pg *postgres.Postgres) *Repo { return &Repo{Postgres: pg} }

type scanner interface {
	Scan(dest ...any) error
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
				return nil // duplicate delivery
			}
		}

		return fn(tx)
	})
}

func addOutbox(ctx context.Context, tx pgx.Tx, aggregate, id, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox marshal: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES ($1, $2, $3, $4)`,
		aggregate, id, eventType, b)
	if err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}

	return nil
}

// auditMetadata folds the actor role and reason into the stored metadata (NXL-F §16).
func auditMetadata(e entity.AuditEntry) ([]byte, error) {
	m := make(map[string]any, len(e.Metadata)+2) //nolint:mnd // + actorRole, reason
	for k, v := range e.Metadata {
		m[k] = v
	}

	m["actorRole"] = e.ActorRole
	if e.Reason != nil {
		m["reason"] = *e.Reason
	}

	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("audit metadata: %w", err)
	}

	return b, nil
}

// insertAudit writes one audit row; a row with a source event id already stored is skipped.
func insertAudit(ctx context.Context, tx pgx.Tx, e entity.AuditEntry) (bool, error) {
	meta, err := auditMetadata(e)
	if err != nil {
		return false, err
	}

	source := e.SourceService
	if source == "" {
		source = _source
	}

	tag, err := tx.Exec(ctx, `INSERT INTO audit_logs
		(actor_id, action, resource_type, resource_id, metadata, source_service, source_event_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source_event_id) WHERE source_event_id IS NOT NULL DO NOTHING`,
		e.ActorID, e.Action, e.ResourceType, e.ResourceID, meta, entity.Excerpt(source, 20), e.SourceEventID) //nolint:mnd // column size
	if err != nil {
		return false, fmt.Errorf("audit insert: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

// where collects SQL conditions with numbered arguments.
type where struct {
	conds []string
	args  []any
}

// add appends a condition; each "?" becomes the next $n placeholder bound to the matching arg.
func (w *where) add(cond string, args ...any) {
	for _, a := range args {
		w.args = append(w.args, a)
		cond = strings.Replace(cond, "?", "$"+strconv.Itoa(len(w.args)), 1)
	}

	w.conds = append(w.conds, cond)
}

// arg binds one more argument and returns its placeholder.
func (w *where) arg(a any) string {
	w.args = append(w.args, a)

	return "$" + strconv.Itoa(len(w.args))
}

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}

	return " WHERE " + strings.Join(w.conds, " AND ")
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.ErrNotFound
	}

	return err
}

// likePrefix escapes LIKE wildcards of a search term.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// PurgeProcessed deletes processed_events rows older than retention.
func (r *Repo) PurgeProcessed(ctx context.Context, retention time.Duration) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `DELETE FROM processed_events WHERE processed_at < now() - $1::interval`,
		retention.String())
	if err != nil {
		return 0, fmt.Errorf("Repo - PurgeProcessed: %w", err)
	}

	return tag.RowsAffected(), nil
}
