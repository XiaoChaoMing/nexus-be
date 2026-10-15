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

// TokenRepo -.
type TokenRepo struct {
	*postgres.Postgres
}

// NewTokenRepo -.
func NewTokenRepo(pg *postgres.Postgres) repo.TokenRepo {
	return &TokenRepo{pg}
}

// Stats -.
func (r *TokenRepo) Stats(ctx context.Context, userID string, purpose entity.TokenPurpose, windowStart time.Time) (entity.TokenStats, error) {
	return tokenStats(ctx, r.Pool, userID, purpose, windowStart)
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func tokenStats(ctx context.Context, q rowQuerier, userID string, purpose entity.TokenPurpose, windowStart time.Time) (entity.TokenStats, error) {
	var s entity.TokenStats

	err := q.QueryRow(ctx,
		`SELECT max(created_at),
		        min(created_at) FILTER (WHERE created_at > $3),
		        count(*) FILTER (WHERE created_at > $3)
		 FROM email_tokens WHERE user_id = $1 AND purpose = $2`,
		userID, string(purpose), windowStart).
		Scan(&s.LastIssuedAt, &s.OldestInWindow, &s.IssuedInWindow)
	if err != nil {
		return entity.TokenStats{}, fmt.Errorf("TokenRepo - Stats - Scan: %w", err)
	}

	return s, nil
}

// Issue -.
func (r *TokenRepo) Issue(ctx context.Context, user entity.User, t repo.NewEmailToken) error {
	return inTx(ctx, r.Pool, "TokenRepo - Issue", func(tx pgx.Tx) error {
		return issueToken(ctx, tx, user, t)
	})
}

// IssueLimited -.
func (r *TokenRepo) IssueLimited(ctx context.Context, user entity.User, t repo.NewEmailToken, limit entity.TokenLimit) (time.Duration, error) {
	var wait time.Duration

	err := inTx(ctx, r.Pool, "TokenRepo - IssueLimited", func(tx pgx.Tx) error {
		// The user row lock serializes concurrent issues for one user; other users are not blocked.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, user.ID); err != nil {
			return fmt.Errorf("lock user: %w", err)
		}

		stats, err := tokenStats(ctx, tx, user.ID, t.Purpose, t.CreatedAt.Add(-limit.Window))
		if err != nil {
			return err
		}

		if wait = limit.RetryAfter(stats, t.CreatedAt); wait > 0 {
			return nil
		}

		return issueToken(ctx, tx, user, t)
	})
	if err != nil {
		return 0, err
	}

	return wait, nil
}

// issueToken supersedes the user's unused tokens of the same purpose (one valid link at a time),
// stores the new one and queues the e-mail.
func issueToken(ctx context.Context, tx pgx.Tx, user entity.User, t repo.NewEmailToken) error {
	_, err := tx.Exec(ctx,
		`UPDATE email_tokens SET used_at = $3 WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`,
		user.ID, string(t.Purpose), t.CreatedAt)
	if err != nil {
		return fmt.Errorf("issueToken - supersede: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO email_tokens (user_id, token_hash, purpose, expires_at, created_at) VALUES ($1, $2, $3, $4, $5)`,
		user.ID, t.Hash, string(t.Purpose), t.ExpiresAt, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("issueToken - insert: %w", err)
	}

	err = addOutbox(ctx, tx, _aggregateUser, user.ID, events.TypeEmailRequested, events.EmailRequested{
		UserID:   user.ID,
		Email:    user.Email,
		Username: user.Username,
		Purpose:  string(t.Purpose),
		Token:    t.Raw,
		Locale:   user.Locale,
	})
	if err != nil {
		return fmt.Errorf("issueToken - %w", err)
	}

	return nil
}

// consumeToken marks a valid token used and returns its user; the WHERE clause makes it one-time
// even under concurrent use.
func consumeToken(ctx context.Context, tx pgx.Tx, hash string, purpose entity.TokenPurpose, now time.Time) (string, error) {
	var userID string

	err := tx.QueryRow(ctx,
		`UPDATE email_tokens SET used_at = $3
		 WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > $3
		 RETURNING user_id`, hash, string(purpose), now).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entity.ErrTokenInvalid
	}

	if err != nil {
		return "", fmt.Errorf("consumeToken - update: %w", err)
	}

	return userID, nil
}

// VerifyEmail -.
func (r *TokenRepo) VerifyEmail(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	var userID string

	err := inTx(ctx, r.Pool, "TokenRepo - VerifyEmail", func(tx pgx.Tx) error {
		var err error

		userID, err = consumeToken(ctx, tx, tokenHash, entity.TokenVerify, now)
		if err != nil {
			return err
		}

		tag, err := tx.Exec(ctx,
			`UPDATE users SET email_verified_at = $2, updated_at = $2 WHERE id = $1 AND email_verified_at IS NULL`,
			userID, now)
		if err != nil {
			return fmt.Errorf("TokenRepo - VerifyEmail - users: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return nil
		}

		err = addOutbox(ctx, tx, _aggregateUser, userID, events.TypeUserEmailVerified, events.UserEmailVerified{UserID: userID})
		if err != nil {
			return fmt.Errorf("TokenRepo - VerifyEmail - %w", err)
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	return userID, nil
}

// ResetPassword -.
func (r *TokenRepo) ResetPassword(ctx context.Context, tokenHash, passwordHash string, now time.Time) (string, error) {
	var userID string

	err := inTx(ctx, r.Pool, "TokenRepo - ResetPassword", func(tx pgx.Tx) error {
		var err error

		userID, err = consumeToken(ctx, tx, tokenHash, entity.TokenReset, now)
		if err != nil {
			return err
		}

		if err := setPassword(ctx, tx, userID, passwordHash, now); err != nil {
			return err
		}

		// Lifts a failed-login lock only; an admin lock stays.
		_, err = tx.Exec(ctx, `UPDATE users SET
				failed_logins = 0,
				last_failed_login_at = NULL,
				status = CASE WHEN lock_kind = 'FAILED_LOGIN' THEN 'ACTIVE' ELSE status END,
				locked_until = CASE WHEN lock_kind = 'FAILED_LOGIN' THEN NULL ELSE locked_until END,
				lock_kind = CASE WHEN lock_kind = 'FAILED_LOGIN' THEN NULL ELSE lock_kind END,
				updated_at = $2
			WHERE id = $1`, userID, now)
		if err != nil {
			return fmt.Errorf("TokenRepo - ResetPassword - users: %w", err)
		}

		return addAudit(ctx, tx, userID, auditOf(&userID, events.ActorUser, _auditPasswordReset, _resourceUser, &userID, nil))
	})
	if err != nil {
		return "", err
	}

	return userID, nil
}
