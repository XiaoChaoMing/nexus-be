// Package persistent implements the identity repositories on PostgreSQL (database nxl_identity).
package persistent

import (
	"context"
	"encoding/json"
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
	_usernameConstraint = "users_username_key"
	_emailConstraint    = "users_email_key"
	// _maxFailedLogins keeps users.failed_logins inside smallint.
	_maxFailedLogins = 32767

	// _userColumns are read by scanUser, in order; the query must alias users as u and LEFT JOIN
	// user_channels as uc.
	_userColumns = `u.id, u.username::text, u.display_name, u.email::text, u.email_verified_at, u.birth_date,
		u.locale, u.timezone, u.global_role, u.status, u.locked_until, u.failed_logins, u.last_failed_login_at,
		u.preferences, u.created_at, u.updated_at, uc.channel_id::text`
	_userFrom   = ` FROM users u LEFT JOIN user_channels uc ON uc.user_id = u.id`
	_userSelect = `SELECT ` + _userColumns + _userFrom
)

// UserRepo -.
type UserRepo struct {
	*postgres.Postgres
}

// NewUserRepo -.
func NewUserRepo(pg *postgres.Postgres) repo.UserRepo {
	return &UserRepo{pg}
}

// Create -.
func (r *UserRepo) Create(ctx context.Context, u entity.User, passwordHash string, verify repo.NewEmailToken) error {
	return inTx(ctx, r.Pool, "UserRepo - Create", func(tx pgx.Tx) error {
		if err := insertUser(ctx, tx, u, passwordHash); err != nil {
			return err
		}

		err := addOutbox(ctx, tx, _aggregateUser, u.ID, events.TypeUserRegistered, events.UserRegistered{
			UserID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email,
		})
		if err != nil {
			return fmt.Errorf("UserRepo - Create - %w", err)
		}

		return issueToken(ctx, tx, u, verify)
	})
}

func insertUser(ctx context.Context, tx pgx.Tx, u entity.User, passwordHash string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO users (id, username, display_name, email, email_verified_at, locale, timezone, global_role,
		                    status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		u.ID, u.Username, u.DisplayName, u.Email, u.EmailVerifiedAt, u.Locale, u.Timezone, u.GlobalRole,
		string(u.Status), u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return mapUniqueViolation(fmt.Errorf("UserRepo - insert users: %w", err))
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, $2, $3)`,
		u.ID, passwordHash, u.CreatedAt)
	if err != nil {
		return fmt.Errorf("UserRepo - insert credentials: %w", err)
	}

	return nil
}

// GetByLogin -.
func (r *UserRepo) GetByLogin(ctx context.Context, login string) (entity.User, string, error) {
	var (
		u    entity.User
		hash string
	)

	// citext columns make both comparisons case-insensitive.
	err := scanUser(r.Pool.QueryRow(ctx,
		`SELECT `+_userColumns+`, c.password_hash`+_userFrom+` JOIN credentials c ON c.user_id = u.id
		 WHERE u.email = $1::citext OR u.username = $1::citext LIMIT 1`, login), &u, &hash)
	if err != nil {
		return entity.User{}, "", err
	}

	return u, hash, nil
}

// GetByID -.
func (r *UserRepo) GetByID(ctx context.Context, id string) (entity.User, error) {
	return getUser(ctx, r.Pool, `u.id = $1`, id)
}

// GetByEmail -.
func (r *UserRepo) GetByEmail(ctx context.Context, email string) (entity.User, error) {
	return getUser(ctx, r.Pool, `u.email = $1::citext`, email)
}

// PasswordHash -.
func (r *UserRepo) PasswordHash(ctx context.Context, userID string) (string, error) {
	var hash string

	err := r.Pool.QueryRow(ctx, `SELECT password_hash FROM credentials WHERE user_id = $1`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entity.ErrUserNotFound
	}

	if err != nil {
		return "", fmt.Errorf("UserRepo - PasswordHash - Scan: %w", err)
	}

	return hash, nil
}

// RecordLoginFailure counts in one UPDATE, so concurrent wrong passwords cannot lose an increment: a
// failure older than the window restarts the count at 1.
func (r *UserRepo) RecordLoginFailure(ctx context.Context, userID string, now time.Time, p repo.LoginFailurePolicy) (entity.User, error) {
	const q = `WITH u AS (
		UPDATE users SET
			failed_logins = LEAST(CASE WHEN last_failed_login_at > $3::timestamptz THEN failed_logins + 1 ELSE 1 END, $6::int),
			last_failed_login_at = $2::timestamptz,
			status = CASE WHEN (CASE WHEN last_failed_login_at > $3::timestamptz THEN failed_logins + 1 ELSE 1 END) >= $5::int
			              THEN 'LOCKED' ELSE 'ACTIVE' END,
			locked_until = CASE WHEN (CASE WHEN last_failed_login_at > $3::timestamptz THEN failed_logins + 1 ELSE 1 END) >= $5::int
			                    THEN $4::timestamptz END,
			updated_at = $2::timestamptz
		WHERE id = $1 AND (status = 'ACTIVE' OR (status = 'LOCKED' AND locked_until IS NOT NULL AND locked_until <= $2::timestamptz))
		RETURNING *
	) SELECT ` + _userColumns + ` FROM u LEFT JOIN user_channels uc ON uc.user_id = u.id`

	var u entity.User

	err := scanUser(r.Pool.QueryRow(ctx, q, userID, now, now.Add(-p.Window), now.Add(p.Window), p.MaxFailures, _maxFailedLogins), &u)
	if err != nil {
		return entity.User{}, err
	}

	return u, nil
}

// ResetLoginFailures -.
func (r *UserRepo) ResetLoginFailures(ctx context.Context, userID string, now time.Time) error {
	_, err := r.Pool.Exec(ctx, `UPDATE users SET
			failed_logins = 0,
			last_failed_login_at = NULL,
			status = CASE WHEN status = 'LOCKED' AND locked_until <= $2 THEN 'ACTIVE' ELSE status END,
			locked_until = CASE WHEN status = 'LOCKED' AND locked_until <= $2 THEN NULL ELSE locked_until END,
			updated_at = $2
		WHERE id = $1`, userID, now)
	if err != nil {
		return fmt.Errorf("UserRepo - ResetLoginFailures - r.Pool.Exec: %w", err)
	}

	return nil
}

// UpdateProfile -.
func (r *UserRepo) UpdateProfile(ctx context.Context, userID string, p repo.ProfilePatch, now time.Time) (entity.User, error) {
	var out entity.User

	err := inTx(ctx, r.Pool, "UserRepo - UpdateProfile", func(tx pgx.Tx) error {
		oldName, err := lockProfile(ctx, tx, userID, p)
		if err != nil {
			return err
		}

		if perr := applyProfile(ctx, tx, userID, p, now); perr != nil {
			return perr
		}

		if p.DisplayName != nil && *p.DisplayName != oldName {
			err = addOutbox(ctx, tx, _aggregateUser, userID, events.TypeUserProfileUpdated,
				events.UserProfileUpdated{UserID: userID, DisplayName: *p.DisplayName})
			if err != nil {
				return fmt.Errorf("UserRepo - UpdateProfile - %w", err)
			}
		}

		out, err = getUser(ctx, tx, `u.id = $1`, userID)

		return err
	})
	if err != nil {
		return entity.User{}, err
	}

	return out, nil
}

// lockProfile locks the user row, enforces that a birth date is set only once and returns the current
// display name.
func lockProfile(ctx context.Context, tx pgx.Tx, userID string, p repo.ProfilePatch) (string, error) {
	var (
		name      string
		birthDate *time.Time
	)

	err := tx.QueryRow(ctx, `SELECT display_name, birth_date FROM users WHERE id = $1 FOR UPDATE`, userID).
		Scan(&name, &birthDate)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entity.ErrUserNotFound
	}

	if err != nil {
		return "", fmt.Errorf("UserRepo - UpdateProfile - select: %w", err)
	}

	if p.BirthDate != nil && birthDate != nil {
		return "", entity.ErrBirthDateAlreadySet
	}

	return name, nil
}

func applyProfile(ctx context.Context, tx pgx.Tx, userID string, p repo.ProfilePatch, now time.Time) error {
	prefs := p.Preferences
	if prefs == nil {
		prefs = map[string]bool{}
	}

	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("UserRepo - UpdateProfile - json.Marshal: %w", err)
	}

	_, err = tx.Exec(ctx, `UPDATE users SET
			display_name = COALESCE($2, display_name),
			birth_date = COALESCE($3::date, birth_date),
			locale = COALESCE($4, locale),
			timezone = COALESCE($5, timezone),
			preferences = preferences || $6::jsonb,
			updated_at = $7
		WHERE id = $1`,
		userID, p.DisplayName, p.BirthDate, p.Locale, p.Timezone, string(prefsJSON), now)
	if err != nil {
		return fmt.Errorf("UserRepo - UpdateProfile - update: %w", err)
	}

	return nil
}

// ChangePassword -.
func (r *UserRepo) ChangePassword(ctx context.Context, userID, passwordHash string, next entity.Session) error {
	return inTx(ctx, r.Pool, "UserRepo - ChangePassword", func(tx pgx.Tx) error {
		if err := setPassword(ctx, tx, userID, passwordHash, next.CreatedAt); err != nil {
			return err
		}

		_, err := tx.Exec(ctx, _insertSession,
			next.ID, next.UserID, next.RefreshTokenHash, next.UserAgent, next.IP, next.CreatedAt, next.ExpiresAt)
		if err != nil {
			return fmt.Errorf("UserRepo - ChangePassword - insert session: %w", err)
		}

		return nil
	})
}

// setPassword stores the hash and ends every session of the user.
func setPassword(ctx context.Context, tx pgx.Tx, userID, passwordHash string, now time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE credentials SET password_hash = $2, updated_at = $3 WHERE user_id = $1`,
		userID, passwordHash, now)
	if err != nil {
		return fmt.Errorf("setPassword - credentials: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return entity.ErrUserNotFound
	}

	// Deleted rather than marked revoked: a device still holding one of these refresh tokens must get a plain
	// "session expired", not trigger reuse detection, which would also revoke the session that changed the password.
	_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("setPassword - delete sessions: %w", err)
	}

	return nil
}

// PromoteAdmin -.
func (r *UserRepo) PromoteAdmin(ctx context.Context, email string) (entity.User, error) {
	var id string

	err := r.Pool.QueryRow(ctx,
		`UPDATE users SET global_role = $2, updated_at = now() WHERE email = $1::citext RETURNING id`,
		email, entity.RoleAdmin).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.User{}, entity.ErrUserNotFound
	}

	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - PromoteAdmin - update: %w", err)
	}

	return r.GetByID(ctx, id)
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// getUser loads one user; cond is a fixed SQL condition on alias u with a single $1 placeholder.
func getUser(ctx context.Context, q querier, cond string, arg any) (entity.User, error) {
	var u entity.User

	if err := scanUser(q.QueryRow(ctx, _userSelect+` WHERE `+cond, arg), &u); err != nil {
		return entity.User{}, err
	}

	return u, nil
}

func scanUser(row pgx.Row, u *entity.User, extra ...any) error {
	var (
		status string
		prefs  []byte
	)

	dest := append([]any{
		&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.EmailVerifiedAt, &u.BirthDate,
		&u.Locale, &u.Timezone, &u.GlobalRole, &status, &u.LockedUntil, &u.FailedLogins, &u.LastFailedLoginAt,
		&prefs, &u.CreatedAt, &u.UpdatedAt, &u.ChannelID,
	}, extra...)

	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.ErrUserNotFound
	}

	if err != nil {
		return fmt.Errorf("UserRepo - scanUser - row.Scan: %w", err)
	}

	u.Status = entity.UserStatus(status)

	if len(prefs) > 0 {
		if err = json.Unmarshal(prefs, &u.Preferences); err != nil {
			return fmt.Errorf("UserRepo - scanUser - preferences: %w", err)
		}
	}

	return nil
}

func mapUniqueViolation(err error) error {
	code, constraint := pgErrorCode(err)
	if code != _uniqueViolation {
		return err
	}

	switch constraint {
	case _usernameConstraint:
		return entity.ErrUsernameTaken
	case _emailConstraint:
		return entity.ErrEmailTaken
	default:
		return err
	}
}

// SessionRepo -.
type SessionRepo struct {
	*postgres.Postgres
}

// NewSessionRepo -.
func NewSessionRepo(pg *postgres.Postgres) repo.SessionRepo {
	return &SessionRepo{pg}
}

const _insertSession = `INSERT INTO sessions (id, user_id, refresh_token_hash, user_agent, ip, created_at, expires_at)
	VALUES ($1, $2, $3, $4, NULLIF($5, '')::inet, $6, $7)`

// Create -.
func (r *SessionRepo) Create(ctx context.Context, s entity.Session) error {
	_, err := r.Pool.Exec(ctx, _insertSession,
		s.ID, s.UserID, s.RefreshTokenHash, s.UserAgent, s.IP, s.CreatedAt, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("SessionRepo - Create - r.Pool.Exec: %w", err)
	}

	return nil
}

// GetByTokenHash -.
func (r *SessionRepo) GetByTokenHash(ctx context.Context, hash string) (entity.Session, error) {
	var (
		s         entity.Session
		userAgent *string
		ip        *string
	)

	err := r.Pool.QueryRow(ctx,
		`SELECT id, user_id, refresh_token_hash, user_agent, host(ip), created_at, expires_at, revoked_at
		 FROM sessions WHERE refresh_token_hash = $1`, hash).
		Scan(&s.ID, &s.UserID, &s.RefreshTokenHash, &userAgent, &ip, &s.CreatedAt, &s.ExpiresAt, &s.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Session{}, entity.ErrSessionNotFound
	}

	if err != nil {
		return entity.Session{}, fmt.Errorf("SessionRepo - GetByTokenHash - Scan: %w", err)
	}

	if userAgent != nil {
		s.UserAgent = *userAgent
	}

	if ip != nil {
		s.IP = *ip
	}

	return s, nil
}

// Rotate -.
func (r *SessionRepo) Rotate(ctx context.Context, oldID string, next entity.Session) error {
	return inTx(ctx, r.Pool, "SessionRepo - Rotate", func(tx pgx.Tx) error {
		// The revoked_at IS NULL guard makes two concurrent refreshes with the same token race safely: only one wins.
		tag, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, oldID, next.CreatedAt)
		if err != nil {
			return fmt.Errorf("SessionRepo - Rotate - revoke: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return entity.ErrSessionAlreadyRevoked
		}

		_, err = tx.Exec(ctx, _insertSession,
			next.ID, next.UserID, next.RefreshTokenHash, next.UserAgent, next.IP, next.CreatedAt, next.ExpiresAt)
		if err != nil {
			return fmt.Errorf("SessionRepo - Rotate - insert: %w", err)
		}

		return nil
	})
}

// Revoke -.
func (r *SessionRepo) Revoke(ctx context.Context, id string) error {
	_, err := r.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("SessionRepo - Revoke - r.Pool.Exec: %w", err)
	}

	return nil
}

// RevokeAllForUser -.
func (r *SessionRepo) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.Pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("SessionRepo - RevokeAllForUser - r.Pool.Exec: %w", err)
	}

	return nil
}
