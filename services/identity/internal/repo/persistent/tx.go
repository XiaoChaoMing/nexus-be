package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minhnn/nexuslive-be/pkg/events"
)

const (
	_uniqueViolation     = "23505"
	_foreignKeyViolation = "23503"
	// _consumer is identity's name in processed_events.
	_consumer = "identity-svc"

	_aggregateUser        = "user"
	_aggregateApplication = "creator_application"

	// Audit actions of audit.recorded.v1 (GĐ5 TSD §6.5) and their resource types.
	_auditTempLocked       = "ACCOUNT_TEMP_LOCKED"
	_auditPasswordChanged  = "PASSWORD_CHANGED"
	_auditPasswordReset    = "PASSWORD_RESET"
	_auditCreatorApproved  = "CREATOR_APPROVED"
	_auditCreatorRejected  = "CREATOR_REJECTED"
	_auditAdminPromoted    = "ADMIN_PROMOTED"
	_resourceUser          = "USER"
	_resourceCreatorApplic = "CREATOR_APPLICATION"
)

func inTx(ctx context.Context, pool *pgxpool.Pool, op string, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s - begin: %w", op, err)
	}

	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // rollback after commit is a no-op

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s - commit: %w", op, err)
	}

	return nil
}

func addOutbox(ctx context.Context, tx pgx.Tx, aggregateType, aggregateID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox %s - json.Marshal: %w", eventType, err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES ($1, $2, $3, $4)`,
		aggregateType, aggregateID, eventType, b)
	if err != nil {
		return fmt.Errorf("outbox %s - insert: %w", eventType, err)
	}

	return nil
}

// pgErrorCode returns the SQLSTATE and constraint of a PostgreSQL error ("" when err is not one).
func pgErrorCode(err error) (code, constraint string) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", ""
	}

	return pgErr.Code, pgErr.ConstraintName
}

func auditOf(actorID *string, actorRole, action, resourceType string, resourceID *string, meta map[string]any) events.AuditRecorded {
	if meta == nil {
		meta = map[string]any{}
	}

	return events.AuditRecorded{
		ActorID: actorID, ActorRole: actorRole, Action: action, ResourceType: resourceType, ResourceID: resourceID, Metadata: meta,
	}
}

// addAudit queues audit.recorded.v1 for safety-svc's audit log in the caller's transaction.
func addAudit(ctx context.Context, tx pgx.Tx, aggregateID string, a events.AuditRecorded) error {
	if err := addOutbox(ctx, tx, _aggregateUser, aggregateID, events.TypeAuditRecorded, a); err != nil {
		return fmt.Errorf("audit %s - %w", a.Action, err)
	}

	return nil
}
