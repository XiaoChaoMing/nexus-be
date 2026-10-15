// Package auth implements registration, sign-in, refresh-token sessions and account self-service.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

const (
	_refreshTokenBytes = 32
	_emailTokenBytes   = 32

	_defaultVerifyTTL    = 24 * time.Hour
	_defaultResetTTL     = 30 * time.Minute
	_defaultMaxFailures  = 10
	_defaultLockDuration = 15 * time.Minute
)

type (
	// PasswordHasher -.
	PasswordHasher interface {
		Hash(password string) (string, error)
		Verify(password, encoded string) (bool, error)
	}

	// TokenSigner issues access tokens.
	TokenSigner interface {
		Sign(userID, role, sessionID string, now time.Time) (string, error)
		TTL() time.Duration
	}

	// Repos are the stores the use case works on.
	Repos struct {
		Users    repo.UserRepo
		Sessions repo.SessionRepo
		Tokens   repo.TokenRepo
	}

	// Config holds the account policies; zero values fall back to the NXL-D defaults.
	Config struct {
		RefreshTTL        time.Duration
		EmailVerifyTTL    time.Duration
		PasswordResetTTL  time.Duration
		LoginMaxFailures  int
		LoginLockDuration time.Duration
		// Location decides which calendar day is "today" for birth dates (nil = UTC).
		Location *time.Location
	}
)

// UseCase -.
type UseCase struct {
	users    repo.UserRepo
	sessions repo.SessionRepo
	tokens   repo.TokenRepo
	hasher   PasswordHasher
	signer   TokenSigner
	cfg      Config
	limit    entity.TokenLimit
	now      func() time.Time
	l        logger.Interface
	// Verified against unknown logins so that response time does not reveal whether an account exists.
	dummyHash string
}

// New -.
func New(r Repos, hasher PasswordHasher, signer TokenSigner, cfg Config, l logger.Interface) (*UseCase, error) {
	dummy, err := hasher.Hash(uuid.NewString())
	if err != nil {
		return nil, fmt.Errorf("AuthUseCase - New - hasher.Hash: %w", err)
	}

	return &UseCase{
		users:     r.Users,
		sessions:  r.Sessions,
		tokens:    r.Tokens,
		hasher:    hasher,
		signer:    signer,
		cfg:       withDefaults(cfg),
		limit:     entity.TokenLimit{MinInterval: _resendInterval, MaxInWindow: _resendMaxPerWindow, Window: _resendWindow},
		now:       func() time.Time { return time.Now().UTC() },
		l:         l,
		dummyHash: dummy,
	}, nil
}

func withDefaults(cfg Config) Config {
	if cfg.EmailVerifyTTL <= 0 {
		cfg.EmailVerifyTTL = _defaultVerifyTTL
	}

	if cfg.PasswordResetTTL <= 0 {
		cfg.PasswordResetTTL = _defaultResetTTL
	}

	if cfg.LoginMaxFailures <= 0 {
		cfg.LoginMaxFailures = _defaultMaxFailures
	}

	if cfg.LoginLockDuration <= 0 {
		cfg.LoginLockDuration = _defaultLockDuration
	}

	if cfg.Location == nil {
		cfg.Location = time.UTC
	}

	return cfg
}

var _ usecase.Auth = (*UseCase)(nil)

// WithClock replaces the time source; used by tests.
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

// Register -.
func (uc *UseCase) Register(ctx context.Context, in usecase.RegisterInput) (usecase.AuthResult, error) {
	hash, err := uc.hasher.Hash(in.Password)
	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - Register - uc.hasher.Hash: %w", err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - Register - uuid.NewV7: %w", err)
	}

	now := uc.now()

	displayName := in.DisplayName
	if displayName == "" {
		displayName = in.Username
	}

	user := entity.User{
		ID:          id.String(),
		Username:    in.Username,
		DisplayName: displayName,
		Email:       in.Email,
		Locale:      entity.DefaultLocale,
		Timezone:    entity.DefaultTimezone,
		GlobalRole:  entity.RoleUser,
		Status:      entity.UserActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	verify, err := uc.newEmailToken(entity.TokenVerify, now)
	if err != nil {
		return usecase.AuthResult{}, err
	}

	if err = uc.users.Create(ctx, user, hash, verify); err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - Register - uc.users.Create: %w", err)
	}

	return uc.startSession(ctx, user, in.Client)
}

// Login checks a temporary lock before the password (every attempt is refused while it lasts, without
// counting), counts wrong passwords, and reveals a permanent admin lock only after a correct password.
func (uc *UseCase) Login(ctx context.Context, in usecase.LoginInput) (usecase.AuthResult, error) {
	user, hash, err := uc.users.GetByLogin(ctx, in.Login)
	if errors.Is(err, entity.ErrUserNotFound) {
		_, _ = uc.hasher.Verify(in.Password, uc.dummyHash) //nolint:errcheck // timing equalizer only

		return usecase.AuthResult{}, entity.ErrInvalidCredentials
	}

	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - Login - uc.users.GetByLogin: %w", err)
	}

	if err := uc.checkPassword(ctx, user, hash, in.Password); err != nil {
		return usecase.AuthResult{}, err
	}

	return uc.startSession(ctx, user, in.Client)
}

// checkPassword applies the lock rules around the password check and maintains the failure counter.
func (uc *UseCase) checkPassword(ctx context.Context, user entity.User, hash, password string) error {
	now := uc.now()

	if user.CanSignIn() && user.IsTemporarilyLocked(now) {
		return &entity.LockedError{Until: *user.LockedUntil}
	}

	ok, err := uc.hasher.Verify(password, hash)
	if err != nil {
		return fmt.Errorf("AuthUseCase - Login - uc.hasher.Verify: %w", err)
	}

	switch {
	case !user.CanSignIn():
		return entity.ErrInvalidCredentials
	case !ok:
		return uc.loginFailed(ctx, user, now)
	case user.IsPermanentlyLocked():
		return entity.ErrAccountLocked
	case user.FailedLogins == 0 && user.Status != entity.UserLocked:
		return nil
	}

	if err = uc.users.ResetLoginFailures(ctx, user.ID, now); err != nil {
		return fmt.Errorf("AuthUseCase - Login - uc.users.ResetLoginFailures: %w", err)
	}

	return nil
}

// loginFailed counts a wrong password and returns the error for the caller.
func (uc *UseCase) loginFailed(ctx context.Context, user entity.User, now time.Time) error {
	if user.IsPermanentlyLocked() {
		return entity.ErrInvalidCredentials
	}

	updated, err := uc.users.RecordLoginFailure(ctx, user.ID, now, repo.LoginFailurePolicy{
		MaxFailures: uc.cfg.LoginMaxFailures,
		Window:      uc.cfg.LoginLockDuration,
	})
	if errors.Is(err, entity.ErrUserNotFound) {
		// Locked or deleted by a concurrent request in between.
		return entity.ErrInvalidCredentials
	}

	if err != nil {
		return fmt.Errorf("AuthUseCase - Login - uc.users.RecordLoginFailure: %w", err)
	}

	if updated.IsTemporarilyLocked(now) {
		uc.l.Warn("audit=account_locked userID=%s lockedUntil=%s failures=%d",
			user.ID, updated.LockedUntil.UTC().Format(time.RFC3339), updated.FailedLogins)

		return &entity.LockedError{Until: *updated.LockedUntil}
	}

	return entity.ErrInvalidCredentials
}

// Refresh rotates the refresh token. Presenting a token that was already rotated is treated as theft:
// every session of that user is revoked.
func (uc *UseCase) Refresh(ctx context.Context, refreshToken string, client usecase.ClientInfo) (usecase.AuthResult, error) {
	if refreshToken == "" {
		return usecase.AuthResult{}, entity.ErrSessionExpired
	}

	session, err := uc.sessions.GetByTokenHash(ctx, hashToken(refreshToken))
	if errors.Is(err, entity.ErrSessionNotFound) {
		return usecase.AuthResult{}, entity.ErrSessionExpired
	}

	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - Refresh - uc.sessions.GetByTokenHash: %w", err)
	}

	now := uc.now()

	if session.IsRevoked() {
		return usecase.AuthResult{}, uc.handleReuse(ctx, session.UserID)
	}

	if session.IsExpired(now) {
		return usecase.AuthResult{}, uc.expiredSession(ctx, session.UserID, now)
	}

	user, err := uc.activeUser(ctx, session.UserID, now)
	if err != nil {
		return usecase.AuthResult{}, err
	}

	next, token, err := uc.newSession(user.ID, client, now)
	if err != nil {
		return usecase.AuthResult{}, err
	}

	err = uc.sessions.Rotate(ctx, session.ID, next)
	if errors.Is(err, entity.ErrSessionAlreadyRevoked) {
		return usecase.AuthResult{}, uc.handleReuse(ctx, session.UserID)
	}

	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - Refresh - uc.sessions.Rotate: %w", err)
	}

	return uc.result(user, next, token, now)
}

// activeUser loads the session owner. A user who can no longer sign in loses every session.
func (uc *UseCase) activeUser(ctx context.Context, userID string, now time.Time) (entity.User, error) {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return entity.User{}, fmt.Errorf("AuthUseCase - Refresh - uc.users.GetByID: %w", err)
	}

	if user.CanSignIn() && !user.IsLocked(now) {
		return user, nil
	}

	if err = uc.sessions.RevokeAllForUser(ctx, user.ID); err != nil {
		return entity.User{}, fmt.Errorf("AuthUseCase - Refresh - uc.sessions.RevokeAllForUser: %w", err)
	}

	return entity.User{}, sessionEndError(user, now)
}

// expiredSession explains an expired session: an admin lock ends the sessions by expiring them, so the owner
// of a locked account gets the lock (as on sign-in) instead of a plain "session expired".
func (uc *UseCase) expiredSession(ctx context.Context, userID string, now time.Time) error {
	user, err := uc.users.GetByID(ctx, userID)
	if errors.Is(err, entity.ErrUserNotFound) {
		return entity.ErrSessionExpired
	}

	if err != nil {
		return fmt.Errorf("AuthUseCase - Refresh - expired - uc.users.GetByID: %w", err)
	}

	return sessionEndError(user, now)
}

// sessionEndError is ACCOUNT_LOCKED (with the end of a timed lock) for a locked account, else SESSION_EXPIRED.
func sessionEndError(user entity.User, now time.Time) error {
	switch {
	case !user.CanSignIn() || !user.IsLocked(now):
		return entity.ErrSessionExpired
	case user.LockedUntil != nil:
		return &entity.LockedError{Until: *user.LockedUntil}
	default:
		return entity.ErrAccountLocked
	}
}

// Logout revokes the session of the given refresh token; unknown tokens are ignored.
func (uc *UseCase) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}

	session, err := uc.sessions.GetByTokenHash(ctx, hashToken(refreshToken))
	if errors.Is(err, entity.ErrSessionNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("AuthUseCase - Logout - uc.sessions.GetByTokenHash: %w", err)
	}

	if err = uc.sessions.Revoke(ctx, session.ID); err != nil {
		return fmt.Errorf("AuthUseCase - Logout - uc.sessions.Revoke: %w", err)
	}

	return nil
}

// Me -.
func (uc *UseCase) Me(ctx context.Context, userID string) (entity.User, error) {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return entity.User{}, fmt.Errorf("AuthUseCase - Me - uc.users.GetByID: %w", err)
	}

	return user, nil
}

func (uc *UseCase) handleReuse(ctx context.Context, userID string) error {
	uc.l.Warn("audit=refresh_reuse userID=%s: refresh token reuse detected, revoking all sessions", userID)

	if err := uc.sessions.RevokeAllForUser(ctx, userID); err != nil {
		return fmt.Errorf("AuthUseCase - handleReuse - uc.sessions.RevokeAllForUser: %w", err)
	}

	return entity.ErrSessionExpired
}

func (uc *UseCase) startSession(ctx context.Context, user entity.User, client usecase.ClientInfo) (usecase.AuthResult, error) {
	now := uc.now()

	session, token, err := uc.newSession(user.ID, client, now)
	if err != nil {
		return usecase.AuthResult{}, err
	}

	if err = uc.sessions.Create(ctx, session); err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - startSession - uc.sessions.Create: %w", err)
	}

	return uc.result(user, session, token, now)
}

func (uc *UseCase) newSession(userID string, client usecase.ClientInfo, now time.Time) (entity.Session, string, error) {
	token, err := randomToken(_refreshTokenBytes)
	if err != nil {
		return entity.Session{}, "", fmt.Errorf("AuthUseCase - newSession - %w", err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return entity.Session{}, "", fmt.Errorf("AuthUseCase - newSession - uuid.NewV7: %w", err)
	}

	return entity.Session{
		ID:               id.String(),
		UserID:           userID,
		RefreshTokenHash: hashToken(token),
		UserAgent:        client.UserAgent,
		IP:               client.IP,
		CreatedAt:        now,
		ExpiresAt:        now.Add(uc.cfg.RefreshTTL),
	}, token, nil
}

func (uc *UseCase) result(user entity.User, s entity.Session, refreshToken string, now time.Time) (usecase.AuthResult, error) {
	access, err := uc.signer.Sign(user.ID, user.GlobalRole, s.ID, now)
	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - result - uc.signer.Sign: %w", err)
	}

	return usecase.AuthResult{
		User:             user,
		AccessToken:      access,
		AccessExpiresIn:  uc.signer.TTL(),
		RefreshToken:     refreshToken,
		RefreshExpiresAt: s.ExpiresAt,
	}, nil
}

// randomToken returns n random bytes, base64url without padding.
func randomToken(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}
