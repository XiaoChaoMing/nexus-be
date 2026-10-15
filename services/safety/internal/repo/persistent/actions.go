package persistent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
)

const _actionCols = `id::text, admin_id::text, report_id::text, type, target_type, target_id::text, reason, expires_at, created_at`

// Record writes the admin action, its audit row and the safety.* event in one transaction.
func (r *Repo) Record(ctx context.Context, a entity.AdminAction, audit entity.AuditEntry, eventType string,
	payload any,
) (string, error) {
	var id string

	err := r.inTx(ctx, "ActionRepo - Record", func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO admin_actions (admin_id, report_id, type, target_type, target_id, reason, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id::text`,
			a.AdminID, a.ReportID, a.Type, a.TargetType, a.TargetID, a.Reason, a.ExpiresAt).Scan(&id)
		if err != nil {
			return fmt.Errorf("insert: %w", err)
		}

		if audit.Metadata == nil {
			audit.Metadata = map[string]any{}
		}

		audit.Metadata["actionId"] = id

		if _, err := insertAudit(ctx, tx, audit); err != nil {
			return err
		}

		// The payload carries the action id the owning service echoes back (user.locked.actionId …).
		b, err := withActionID(payload, id)
		if err != nil {
			return err
		}

		return addOutbox(ctx, tx, strings.ToLower(a.TargetType), a.TargetID, eventType, b)
	})
	if err != nil {
		return "", fmt.Errorf("ActionRepo - Record: %w", err)
	}

	return id, nil
}

// withActionID sets "actionId" in the JSON object of payload.
func withActionID(payload any, id string) (json.RawMessage, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}

	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("payload object: %w", err)
	}

	m["actionId"] = id

	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}

	return out, nil
}

// ForReport lists actions on the report's target or on the user behind it, newest first.
func (r *Repo) ForReport(ctx context.Context, rep entity.Report, limit int) ([]entity.AdminAction, error) {
	userID := ""
	if rep.TargetUserID != nil {
		userID = *rep.TargetUserID
	}

	rows, err := r.Pool.Query(ctx, `SELECT `+_actionCols+` FROM admin_actions
		WHERE (target_type = $1 AND target_id = $2) OR (target_type = 'USER' AND target_id::text = $3)
		ORDER BY created_at DESC, id DESC LIMIT $4`, rep.TargetType, rep.TargetID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("ActionRepo - ForReport: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.AdminAction, error) {
		var a entity.AdminAction

		err := row.Scan(&a.ID, &a.AdminID, &a.ReportID, &a.Type, &a.TargetType, &a.TargetID, &a.Reason, &a.ExpiresAt,
			&a.CreatedAt)
		a.Admin = entity.Ref{ID: a.AdminID}

		return a, err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("ActionRepo - ForReport - scan: %w", err)
	}

	return out, nil
}

// ReportExists -.
func (r *Repo) ReportExists(ctx context.Context, id string) (bool, error) {
	var ok bool

	if err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reports WHERE id = $1)`, id).Scan(&ok); err != nil {
		return false, fmt.Errorf("ActionRepo - ReportExists: %w", err)
	}

	return ok, nil
}

// Audit implements repo.AuditRepo (its Insert and List differ from the report ones).
type Audit struct{ *Repo }

// Insert stores an audit row from a domain event once; promote sets the copy's role to ADMIN in the
// same transaction (ADMIN_PROMOTED has no domain event of its own).
func (r Audit) Insert(ctx context.Context, e entity.AuditEntry, promote *string) error {
	err := r.inTx(ctx, "AuditRepo - Insert", func(tx pgx.Tx) error {
		added, err := insertAudit(ctx, tx, e)
		if err != nil || !added || promote == nil {
			return err
		}

		_, err = tx.Exec(ctx, `UPDATE users SET global_role = 'ADMIN' WHERE id = $1`, *promote)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("AuditRepo - Insert: %w", err)
	}

	return nil
}

// List is the audit log, newest first.
func (r Audit) List(ctx context.Context, f repo.AuditFilter, after *repo.TimeCursor, limit int) ([]entity.AuditEntry, error) {
	w := &where{}
	if f.ActorID != nil {
		w.add("actor_id = ?", *f.ActorID)
	}

	if f.Action != nil {
		w.add("action = ?", *f.Action)
	}

	if f.ResourceType != nil {
		w.add("resource_type = ?", *f.ResourceType)
	}

	if f.ResourceID != nil {
		w.add("resource_id = ?", *f.ResourceID)
	}

	if f.From != nil {
		w.add("created_at >= ?", *f.From)
	}

	if f.To != nil {
		w.add("created_at < ?", *f.To)
	}

	if after != nil {
		w.add("(created_at, id) < (?, ?)", after.At, after.ID)
	}

	rows, err := r.Pool.Query(ctx, `SELECT id::text, actor_id::text, action, resource_type, resource_id::text, metadata,
		source_service, source_event_id::text, created_at FROM audit_logs`+w.sql()+
		` ORDER BY created_at DESC, id DESC LIMIT `+w.arg(limit), w.args...)
	if err != nil {
		return nil, fmt.Errorf("AuditRepo - List: %w", err)
	}

	out, err := pgx.CollectRows(rows, scanAudit)
	if err != nil {
		return nil, fmt.Errorf("AuditRepo - List - scan: %w", err)
	}

	return out, nil
}

func scanAudit(row pgx.CollectableRow) (entity.AuditEntry, error) {
	var (
		e    entity.AuditEntry
		meta []byte
	)

	if err := row.Scan(&e.ID, &e.ActorID, &e.Action, &e.ResourceType, &e.ResourceID, &meta, &e.SourceService,
		&e.SourceEventID, &e.CreatedAt); err != nil {
		return e, err //nolint:wrapcheck // wrapped by the caller
	}

	e.Metadata = map[string]any{}
	if err := json.Unmarshal(meta, &e.Metadata); err != nil {
		return e, fmt.Errorf("metadata: %w", err)
	}

	if role, ok := e.Metadata["actorRole"].(string); ok {
		e.ActorRole = role
	}

	if reason, ok := e.Metadata["reason"].(string); ok {
		e.Reason = &reason
	}

	delete(e.Metadata, "actorRole")
	delete(e.Metadata, "reason")

	if e.ActorID != nil {
		e.Actor = &entity.Ref{ID: *e.ActorID}
	}

	return e, nil
}
