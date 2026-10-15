package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
)

const (
	_onePendingIndex = "creator_applications_one_pending_idx"

	_appColumns = `a.id, a.user_id, a.status, a.legal_name, a.birth_date, COALESCE(a.note, ''),
		COALESCE(a.reject_reason, ''), a.reviewed_by, a.reviewed_at, a.created_at, a.updated_at`
	_applicantColumns = `u.id, u.username::text, u.display_name, u.email::text, u.email_verified_at, u.created_at`
)

// ApplicationRepo -.
type ApplicationRepo struct {
	*postgres.Postgres
}

// NewApplicationRepo -.
func NewApplicationRepo(pg *postgres.Postgres) repo.ApplicationRepo {
	return &ApplicationRepo{pg}
}

// CreatorState -.
func (r *ApplicationRepo) CreatorState(ctx context.Context, userID string) (repo.CreatorState, error) {
	var s repo.CreatorState

	err := r.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_channels WHERE user_id = $1),
		        EXISTS (SELECT 1 FROM creator_applications WHERE user_id = $1 AND status = 'APPROVED'),
		        EXISTS (SELECT 1 FROM creator_applications WHERE user_id = $1 AND status = 'PENDING')`, userID).
		Scan(&s.HasChannel, &s.HasApproved, &s.HasPending)
	if err != nil {
		return repo.CreatorState{}, fmt.Errorf("ApplicationRepo - CreatorState - Scan: %w", err)
	}

	return s, nil
}

// Create -.
func (r *ApplicationRepo) Create(ctx context.Context, a entity.Application) error {
	return inTx(ctx, r.Pool, "ApplicationRepo - Create", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO creator_applications (id, user_id, legal_name, birth_date, note, status, created_at, updated_at)
			 VALUES ($1, $2, $3, $4::date, NULLIF($5, ''), $6, $7, $7)`,
			a.ID, a.UserID, a.LegalName, a.BirthDate, a.Note, string(entity.ApplicationPending), a.CreatedAt)
		if code, constraint := pgErrorCode(err); code == _uniqueViolation && constraint == _onePendingIndex {
			return entity.ErrApplicationPending
		}

		if err != nil {
			return fmt.Errorf("ApplicationRepo - Create - insert: %w", err)
		}

		err = addOutbox(ctx, tx, _aggregateApplication, a.ID, events.TypeCreatorApplicationSent,
			events.CreatorApplicationSubmitted{ApplicationID: a.ID, UserID: a.UserID})
		if err != nil {
			return fmt.Errorf("ApplicationRepo - Create - %w", err)
		}

		return nil
	})
}

// Latest -.
func (r *ApplicationRepo) Latest(ctx context.Context, userID string) (*entity.Application, error) {
	var a entity.Application

	err := scanApplication(r.Pool.QueryRow(ctx,
		`SELECT `+_appColumns+` FROM creator_applications a WHERE a.user_id = $1
		 ORDER BY a.created_at DESC, a.id DESC LIMIT 1`, userID), &a)
	if errors.Is(err, entity.ErrApplicationNotFound) {
		return nil, nil //nolint:nilnil // no application is a valid answer
	}

	if err != nil {
		return nil, err
	}

	return &a, nil
}

// Cancel -.
func (r *ApplicationRepo) Cancel(ctx context.Context, userID, id string, now time.Time) error {
	tag, err := r.Pool.Exec(ctx,
		`UPDATE creator_applications SET status = 'CANCELLED', updated_at = $3
		 WHERE id = $1 AND user_id = $2 AND status = 'PENDING'`, id, userID, now)
	if err != nil {
		return fmt.Errorf("ApplicationRepo - Cancel - update: %w", err)
	}

	if tag.RowsAffected() == 1 {
		return nil
	}

	var exists bool

	err = r.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM creator_applications WHERE id = $1 AND user_id = $2)`, id, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("ApplicationRepo - Cancel - exists: %w", err)
	}

	if !exists {
		return entity.ErrApplicationNotFound
	}

	return entity.ErrInvalidState
}

// List -.
func (r *ApplicationRepo) List(
	ctx context.Context, status entity.ApplicationStatus, after *repo.ApplicationCursor, limit int,
) ([]entity.Application, error) {
	q := r.Builder.Select(_appColumns+`, `+_applicantColumns).
		From("creator_applications a").
		Join("users u ON u.id = a.user_id").
		OrderBy("a.created_at", "a.id").
		Limit(uint64(max(limit, 0)))

	if status != "" {
		q = q.Where("a.status = ?", string(status))
	}

	if after != nil {
		q = q.Where("(a.created_at, a.id) > (?::timestamptz, ?::uuid)", after.CreatedAt, after.ID)
	}

	sql, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("ApplicationRepo - List - ToSql: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("ApplicationRepo - List - Query: %w", err)
	}
	defer rows.Close()

	out := make([]entity.Application, 0, limit)

	for rows.Next() {
		var a entity.Application

		if err := scanAdminApplication(rows, &a); err != nil {
			return nil, err
		}

		out = append(out, a)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("ApplicationRepo - List - rows: %w", err)
	}

	return out, nil
}

// Get -.
func (r *ApplicationRepo) Get(ctx context.Context, id string) (entity.Application, error) {
	var a entity.Application

	err := scanAdminApplication(r.Pool.QueryRow(ctx,
		`SELECT `+_appColumns+`, `+_applicantColumns+`
		 FROM creator_applications a JOIN users u ON u.id = a.user_id WHERE a.id = $1`, id), &a)
	if err != nil {
		return entity.Application{}, err
	}

	return a, nil
}

// Approve -.
func (r *ApplicationRepo) Approve(ctx context.Context, adminID, id string, now time.Time) error {
	return inTx(ctx, r.Pool, "ApplicationRepo - Approve", func(tx pgx.Tx) error {
		var (
			userID    string
			birthDate time.Time
		)

		// The status guard lets exactly one of two concurrent reviews win.
		err := tx.QueryRow(ctx,
			`UPDATE creator_applications SET status = 'APPROVED', reviewed_by = $2, reviewed_at = $3, updated_at = $3
			 WHERE id = $1 AND status = 'PENDING' RETURNING user_id, birth_date`, id, adminID, now).
			Scan(&userID, &birthDate)
		if errors.Is(err, pgx.ErrNoRows) {
			return notPending(ctx, tx, id)
		}

		if err != nil {
			return fmt.Errorf("ApplicationRepo - Approve - update: %w", err)
		}

		if aerr := approved(ctx, tx, id, userID, birthDate, now); aerr != nil {
			return aerr
		}

		return addAudit(ctx, tx, userID, auditOf(&adminID, events.ActorAdmin, _auditCreatorApproved, _resourceCreatorApplic, &id,
			map[string]any{"userId": userID}))
	})
}

// approved copies the birth date to the account and announces the new creator.
func approved(ctx context.Context, tx pgx.Tx, applicationID, userID string, birthDate, now time.Time) error {
	var username, displayName string

	err := tx.QueryRow(ctx,
		`UPDATE users SET birth_date = $2::date, updated_at = $3 WHERE id = $1 RETURNING username::text, display_name`,
		userID, birthDate, now).Scan(&username, &displayName)
	if err != nil {
		return fmt.Errorf("approved - users: %w", err)
	}

	err = addOutbox(ctx, tx, _aggregateApplication, applicationID, events.TypeCreatorApproved, events.CreatorApproved{
		ApplicationID: applicationID, UserID: userID, Username: username, DisplayName: displayName,
	})
	if err != nil {
		return fmt.Errorf("approved - %w", err)
	}

	return nil
}

// Reject -.
func (r *ApplicationRepo) Reject(ctx context.Context, adminID, id, reason string, now time.Time) error {
	return inTx(ctx, r.Pool, "ApplicationRepo - Reject", func(tx pgx.Tx) error {
		var userID string

		err := tx.QueryRow(ctx,
			`UPDATE creator_applications
			 SET status = 'REJECTED', reject_reason = $4, reviewed_by = $2, reviewed_at = $3, updated_at = $3
			 WHERE id = $1 AND status = 'PENDING' RETURNING user_id`, id, adminID, now, reason).
			Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return notPending(ctx, tx, id)
		}

		if err != nil {
			return fmt.Errorf("ApplicationRepo - Reject - update: %w", err)
		}

		err = addOutbox(ctx, tx, _aggregateApplication, id, events.TypeCreatorRejected, events.CreatorRejected{
			ApplicationID: id, UserID: userID, Reason: reason,
		})
		if err != nil {
			return fmt.Errorf("ApplicationRepo - Reject - %w", err)
		}

		a := auditOf(&adminID, events.ActorAdmin, _auditCreatorRejected, _resourceCreatorApplic, &id, map[string]any{"userId": userID})
		a.Reason = &reason

		return addAudit(ctx, tx, userID, a)
	})
}

// notPending tells a missing application from one that is no longer PENDING.
func notPending(ctx context.Context, tx pgx.Tx, id string) error {
	var exists bool

	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM creator_applications WHERE id = $1)`, id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("notPending - exists: %w", err)
	}

	if !exists {
		return entity.ErrApplicationNotFound
	}

	return entity.ErrInvalidState
}

func scanApplication(row pgx.Row, a *entity.Application, extra ...any) error {
	var status string

	dest := append([]any{
		&a.ID, &a.UserID, &status, &a.LegalName, &a.BirthDate, &a.Note,
		&a.RejectReason, &a.ReviewedBy, &a.ReviewedAt, &a.CreatedAt, &a.UpdatedAt,
	}, extra...)

	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.ErrApplicationNotFound
	}

	if err != nil {
		return fmt.Errorf("ApplicationRepo - scanApplication - Scan: %w", err)
	}

	a.Status = entity.ApplicationStatus(status)

	return nil
}

// scanAdminApplication reads _appColumns followed by _applicantColumns.
func scanAdminApplication(row pgx.Row, a *entity.Application) error {
	var u entity.User

	err := scanApplication(row, a, &u.ID, &u.Username, &u.DisplayName, &u.Email, &u.EmailVerifiedAt, &u.CreatedAt)
	if err != nil {
		return err
	}

	a.Applicant = &u

	return nil
}
