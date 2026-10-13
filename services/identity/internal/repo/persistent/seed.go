package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
)

// SeedRepo -.
type SeedRepo struct {
	*postgres.Postgres
}

// NewSeedRepo -.
func NewSeedRepo(pg *postgres.Postgres) repo.SeedRepo {
	return &SeedRepo{pg}
}

// EnsureUser -.
func (r *SeedRepo) EnsureUser(ctx context.Context, u entity.User, passwordHash string) (entity.User, bool, error) {
	created := false

	err := inTx(ctx, r.Pool, "SeedRepo - EnsureUser", func(tx pgx.Tx) error {
		var exists bool

		err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE username = $1::citext OR email = $2::citext)`,
			u.Username, u.Email).Scan(&exists)
		if err != nil {
			return fmt.Errorf("SeedRepo - EnsureUser - exists: %w", err)
		}

		if exists {
			return nil
		}

		if err := insertUser(ctx, tx, u, passwordHash); err != nil {
			return err
		}

		created = true

		return nil
	})
	if err != nil {
		return entity.User{}, false, err
	}

	stored, err := getUser(ctx, r.Pool, `u.email = $1::citext`, u.Email)
	if errors.Is(err, entity.ErrUserNotFound) {
		stored, err = getUser(ctx, r.Pool, `u.username = $1::citext`, u.Username)
	}

	if err != nil {
		return entity.User{}, false, err
	}

	return stored, created, nil
}

// EnsureApproved -.
func (r *SeedRepo) EnsureApproved(ctx context.Context, a entity.Application, adminID string) (bool, error) {
	created := false

	err := inTx(ctx, r.Pool, "SeedRepo - EnsureApproved", func(tx pgx.Tx) error {
		var done bool

		err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM creator_applications WHERE user_id = $1 AND status = 'APPROVED')
			     OR EXISTS (SELECT 1 FROM user_channels WHERE user_id = $1)`, a.UserID).Scan(&done)
		if err != nil {
			return fmt.Errorf("SeedRepo - EnsureApproved - exists: %w", err)
		}

		if done {
			return nil
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO creator_applications
			   (id, user_id, legal_name, birth_date, note, status, reviewed_by, reviewed_at, created_at, updated_at)
			 VALUES ($1, $2, $3, $4::date, NULLIF($5, ''), 'APPROVED', $6, $7, $7, $7)`,
			a.ID, a.UserID, a.LegalName, a.BirthDate, a.Note, adminID, a.CreatedAt)
		if err != nil {
			return fmt.Errorf("SeedRepo - EnsureApproved - insert: %w", err)
		}

		created = true

		return approved(ctx, tx, a.ID, a.UserID, a.BirthDate, a.CreatedAt)
	})
	if err != nil {
		return false, err
	}

	return created, nil
}
