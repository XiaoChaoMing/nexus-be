package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
)

// _reportCols is shared by every query that returns entity.Report (alias r).
const _reportCols = `r.id::text, r.reporter_id::text, r.target_type, r.target_id::text, r.target_user_id::text,
	r.stream_id::text, r.reason, r.note, r.evidence, r.evidence_status, r.status, r.assigned_to::text,
	r.resolved_by::text, r.resolved_at, r.resolution_note, r.created_at, r.updated_at,
	(SELECT count(*) FROM reports s WHERE s.target_type = r.target_type AND s.target_id = r.target_id
		AND s.status IN ` + _open + `)`

func scanReport(row scanner) (entity.Report, error) {
	var (
		r                    entity.Report
		evidence             []byte
		assigned, resolvedBy *string
	)

	err := row.Scan(&r.ID, &r.ReporterID, &r.TargetType, &r.TargetID, &r.TargetUserID, &r.StreamID, &r.Reason,
		&r.Note, &evidence, &r.EvidenceStatus, &r.Status, &assigned, &resolvedBy, &r.ResolvedAt,
		&r.ResolutionNote, &r.CreatedAt, &r.UpdatedAt, &r.SameTarget)
	if err != nil {
		return entity.Report{}, err //nolint:wrapcheck // callers wrap
	}

	r.Reporter = entity.Ref{ID: r.ReporterID}
	if assigned != nil {
		r.AssignedTo = &entity.Ref{ID: *assigned}
	}

	if resolvedBy != nil {
		r.ResolvedBy = &entity.Ref{ID: *resolvedBy}
	}

	r.Evidence = []entity.EvidenceItem{}
	if len(evidence) > 0 {
		if err := json.Unmarshal(evidence, &r.Evidence); err != nil {
			return entity.Report{}, fmt.Errorf("evidence: %w", err)
		}
	}

	return r, nil
}

func collectReports(rows pgx.Rows) ([]entity.Report, error) {
	defer rows.Close()

	out := []entity.Report{}

	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, rows.Err() //nolint:wrapcheck // callers wrap
}

func getReport(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, id string,
) (entity.Report, error) {
	r, err := scanReport(q.QueryRow(ctx, `SELECT `+_reportCols+` FROM reports r WHERE r.id = $1`, id))

	return r, notFound(err)
}

// OpenDuplicate -.
func (r *Repo) OpenDuplicate(ctx context.Context, reporterID, targetType, targetID string) (*entity.Report, error) {
	rep, err := scanReport(r.Pool.QueryRow(ctx, `SELECT `+_reportCols+` FROM reports r
		WHERE r.reporter_id = $1 AND r.target_type = $2 AND r.target_id = $3 AND r.status IN `+_open,
		reporterID, targetType, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // no duplicate
	}

	if err != nil {
		return nil, fmt.Errorf("ReportRepo - OpenDuplicate: %w", err)
	}

	return &rep, nil
}

// Insert -.
func (r *Repo) Insert(ctx context.Context, in entity.Report) (entity.Report, bool, error) {
	evidence, err := json.Marshal(in.Evidence)
	if err != nil {
		return entity.Report{}, false, fmt.Errorf("ReportRepo - Insert - evidence: %w", err)
	}

	var id string

	err = r.Pool.QueryRow(ctx, `INSERT INTO reports
		(reporter_id, target_type, target_id, target_user_id, stream_id, reason, note, evidence, evidence_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (reporter_id, target_type, target_id) WHERE status IN `+_open+` DO NOTHING
		RETURNING id::text`,
		in.ReporterID, in.TargetType, in.TargetID, in.TargetUserID, in.StreamID, in.Reason, in.Note, evidence,
		in.EvidenceStatus).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		dup, derr := r.OpenDuplicate(ctx, in.ReporterID, in.TargetType, in.TargetID)
		if derr != nil || dup == nil {
			return entity.Report{}, false, fmt.Errorf("ReportRepo - Insert - duplicate: %w", errors.Join(derr, entity.ErrInvalidState))
		}

		return *dup, false, nil
	}

	if err != nil {
		return entity.Report{}, false, fmt.Errorf("ReportRepo - Insert: %w", err)
	}

	out, err := getReport(ctx, r.Pool, id)
	if err != nil {
		return entity.Report{}, false, fmt.Errorf("ReportRepo - Insert - read: %w", err)
	}

	return out, true, nil
}

// List is the admin queue, oldest first.
func (r *Repo) List(ctx context.Context, f repo.ReportFilter, after *repo.TimeCursor, limit int) ([]entity.Report, error) {
	w := &where{}
	if len(f.Statuses) > 0 {
		w.add("r.status = ANY(?)", f.Statuses)
	}

	if len(f.TargetTypes) > 0 {
		w.add("r.target_type = ANY(?)", f.TargetTypes)
	}

	if len(f.Reasons) > 0 {
		w.add("r.reason = ANY(?)", f.Reasons)
	}

	if after != nil {
		w.add("(r.created_at, r.id) > (?, ?)", after.At, after.ID)
	}

	rows, err := r.Pool.Query(ctx, `SELECT `+_reportCols+` FROM reports r`+w.sql()+
		` ORDER BY r.created_at, r.id LIMIT `+w.arg(limit), w.args...)
	if err != nil {
		return nil, fmt.Errorf("ReportRepo - List: %w", err)
	}

	out, err := collectReports(rows)
	if err != nil {
		return nil, fmt.Errorf("ReportRepo - List - scan: %w", err)
	}

	return out, nil
}

// Counts -.
func (r *Repo) Counts(ctx context.Context) (open, inReview int, err error) {
	err = r.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'OPEN'), count(*) FILTER (WHERE status = 'IN_REVIEW')
		FROM reports WHERE status IN `+_open).Scan(&open, &inReview)
	if err != nil {
		return 0, 0, fmt.Errorf("ReportRepo - Counts: %w", err)
	}

	return open, inReview, nil
}

// Get -.
func (r *Repo) Get(ctx context.Context, id string) (entity.Report, error) {
	rep, err := getReport(ctx, r.Pool, id)
	if err != nil {
		return entity.Report{}, fmt.Errorf("ReportRepo - Get: %w", err)
	}

	return rep, nil
}

// Related lists other reports on the same target, newest first.
func (r *Repo) Related(ctx context.Context, rep entity.Report, limit int) ([]entity.Report, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_reportCols+` FROM reports r
		WHERE r.target_type = $1 AND r.target_id = $2 AND r.id <> $3 ORDER BY r.created_at DESC, r.id DESC LIMIT $4`,
		rep.TargetType, rep.TargetID, rep.ID, limit)
	if err != nil {
		return nil, fmt.Errorf("ReportRepo - Related: %w", err)
	}

	out, err := collectReports(rows)
	if err != nil {
		return nil, fmt.Errorf("ReportRepo - Related - scan: %w", err)
	}

	return out, nil
}

// closedOrMissing explains a conditional UPDATE that changed nothing.
func closedOrMissing(ctx context.Context, tx pgx.Tx, id string) error {
	var status string

	err := tx.QueryRow(ctx, `SELECT status FROM reports WHERE id = $1`, id).Scan(&status)
	if err != nil {
		return notFound(err)
	}

	return entity.ErrInvalidState
}

// Claim sets IN_REVIEW and the assignee (another admin may take the report over).
func (r *Repo) Claim(ctx context.Context, adminID, id string) (entity.Report, error) {
	var out entity.Report

	err := r.inTx(ctx, "ReportRepo - Claim", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE reports SET status = 'IN_REVIEW', assigned_to = $2, updated_at = now()
			WHERE id = $1 AND status IN `+_open, id, adminID)
		if err != nil {
			return fmt.Errorf("ReportRepo - Claim: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return closedOrMissing(ctx, tx, id)
		}

		if _, err := insertAudit(ctx, tx, reportAudit(adminID, id, "REPORT_CLAIMED", nil)); err != nil {
			return err
		}

		out, err = getReport(ctx, tx, id)

		return err
	})
	if err != nil {
		return entity.Report{}, fmt.Errorf("ReportRepo - Claim: %w", err)
	}

	return out, nil
}

func reportAudit(adminID, reportID, action string, note *string) entity.AuditEntry {
	return entity.AuditEntry{
		ActorID: &adminID, ActorRole: events.ActorAdmin, Action: action, ResourceType: "REPORT",
		ResourceID: &reportID, Reason: note, Metadata: map[string]any{},
	}
}

// Resolve closes the report (and, with ApplyToSameTarget, every open report on its target): audit +
// safety.report_resolved per closed row, one transaction.
func (r *Repo) Resolve(ctx context.Context, adminID, id string, in repo.Resolution) (entity.Report, int, error) {
	var (
		out    entity.Report
		closed int
	)

	err := r.inTx(ctx, "ReportRepo - Resolve", func(tx pgx.Tx) error {
		cond := `id = $1`
		if in.ApplyToSameTarget {
			cond = `(target_type, target_id) = (SELECT target_type, target_id FROM reports WHERE id = $1)`
		}

		rows, err := tx.Query(ctx, `UPDATE reports SET status = $2, resolved_by = $3, resolved_at = now(),
			resolution_note = $4, updated_at = now()
			WHERE `+cond+` AND status IN `+_open+` AND EXISTS (SELECT 1 FROM reports WHERE id = $1 AND status IN `+_open+`)
			RETURNING id::text, reporter_id::text, target_type, status`, id, in.Status, adminID, in.Note)
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}

		done, err := pgx.CollectRows(rows, pgx.RowToStructByPos[events.ReportResolved])
		if err != nil {
			return fmt.Errorf("update scan: %w", err)
		}

		if len(done) == 0 {
			return closedOrMissing(ctx, tx, id)
		}

		if err := recordResolved(ctx, tx, adminID, in, done); err != nil {
			return err
		}

		closed = len(done)
		out, err = getReport(ctx, tx, id)

		return err
	})
	if err != nil {
		return entity.Report{}, 0, fmt.Errorf("ReportRepo - Resolve: %w", err)
	}

	return out, closed, nil
}

func recordResolved(ctx context.Context, tx pgx.Tx, adminID string, in repo.Resolution, done []events.ReportResolved) error {
	action := "REPORT_RESOLVED"
	if in.Status == entity.ReportDismissed {
		action = "REPORT_DISMISSED"
	}

	for _, d := range done {
		if _, err := insertAudit(ctx, tx, reportAudit(adminID, d.ReportID, action, &in.Note)); err != nil {
			return err
		}

		if err := addOutbox(ctx, tx, "report", d.ReportID, events.TypeReportResolved, d); err != nil {
			return err
		}
	}

	return nil
}
