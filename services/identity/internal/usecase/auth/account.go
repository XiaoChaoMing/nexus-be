package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

// E-mail token issuing limit per user and purpose (NXL-D AF-D02).
const (
	_resendInterval     = 60 * time.Second
	_resendMaxPerWindow = 5
	_resendWindow       = time.Hour
)

// VerifyEmail -.
func (uc *UseCase) VerifyEmail(ctx context.Context, token string) (entity.User, error) {
	if token == "" {
		return entity.User{}, entity.ErrTokenInvalid
	}

	userID, err := uc.tokens.VerifyEmail(ctx, hashToken(token), uc.now())
	if err != nil {
		return entity.User{}, fmt.Errorf("AuthUseCase - VerifyEmail - uc.tokens.VerifyEmail: %w", err)
	}

	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return entity.User{}, fmt.Errorf("AuthUseCase - VerifyEmail - uc.users.GetByID: %w", err)
	}

	return user, nil
}

// ResendVerification -.
func (uc *UseCase) ResendVerification(ctx context.Context, userID string) error {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("AuthUseCase - ResendVerification - uc.users.GetByID: %w", err)
	}

	if user.IsEmailVerified() {
		return entity.ErrAlreadyVerified
	}

	wait, err := uc.issue(ctx, user, entity.TokenVerify, uc.now())
	if err != nil {
		return err
	}

	if wait > 0 {
		return &entity.RetryError{RetryAfter: wait}
	}

	return nil
}

// ForgotPassword returns nil for unknown accounts and when rate limited, so the response never reveals
// whether the e-mail is registered.
func (uc *UseCase) ForgotPassword(ctx context.Context, email string) error {
	user, err := uc.users.GetByEmail(ctx, email)
	if errors.Is(err, entity.ErrUserNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("AuthUseCase - ForgotPassword - uc.users.GetByEmail: %w", err)
	}

	if !user.CanSignIn() {
		return nil
	}

	wait, err := uc.issue(ctx, user, entity.TokenReset, uc.now())
	if err != nil {
		return err
	}

	if wait > 0 {
		uc.l.Info("AuthUseCase - ForgotPassword - rate limited, no e-mail sent, userID: %s", user.ID)
	}

	return nil
}

// ResetPassword -.
func (uc *UseCase) ResetPassword(ctx context.Context, token, password string) error {
	if token == "" {
		return entity.ErrTokenInvalid
	}

	hash, err := uc.hasher.Hash(password)
	if err != nil {
		return fmt.Errorf("AuthUseCase - ResetPassword - uc.hasher.Hash: %w", err)
	}

	userID, err := uc.tokens.ResetPassword(ctx, hashToken(token), hash, uc.now())
	if err != nil {
		return fmt.Errorf("AuthUseCase - ResetPassword - uc.tokens.ResetPassword: %w", err)
	}

	uc.l.Info("audit=password_reset userID=%s", userID)

	return nil
}

// ChangePassword keeps the caller signed in with a brand-new session; every other session is revoked.
func (uc *UseCase) ChangePassword(ctx context.Context, in usecase.ChangePasswordInput) (usecase.AuthResult, error) {
	hash, err := uc.users.PasswordHash(ctx, in.UserID)
	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - ChangePassword - uc.users.PasswordHash: %w", err)
	}

	ok, err := uc.hasher.Verify(in.CurrentPassword, hash)
	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - ChangePassword - uc.hasher.Verify: %w", err)
	}

	if !ok {
		return usecase.AuthResult{}, entity.ErrWrongPassword
	}

	user, err := uc.users.GetByID(ctx, in.UserID)
	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - ChangePassword - uc.users.GetByID: %w", err)
	}

	newHash, err := uc.hasher.Hash(in.NewPassword)
	if err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - ChangePassword - uc.hasher.Hash: %w", err)
	}

	now := uc.now()

	session, token, err := uc.newSession(user.ID, in.Client, now)
	if err != nil {
		return usecase.AuthResult{}, err
	}

	if err = uc.users.ChangePassword(ctx, user.ID, newHash, session); err != nil {
		return usecase.AuthResult{}, fmt.Errorf("AuthUseCase - ChangePassword - uc.users.ChangePassword: %w", err)
	}

	uc.l.Info("audit=password_changed userID=%s", user.ID)

	return uc.result(user, session, token, now)
}

// Profile -.
func (uc *UseCase) Profile(ctx context.Context, userID string) (usecase.Profile, error) {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return usecase.Profile{}, fmt.Errorf("AuthUseCase - Profile - uc.users.GetByID: %w", err)
	}

	return usecase.Profile{User: user}, nil
}

// UpdateProfile -.
func (uc *UseCase) UpdateProfile(ctx context.Context, userID string, in usecase.ProfileUpdate) (usecase.Profile, error) {
	now := uc.now()

	patch := repo.ProfilePatch{Locale: in.Locale, Timezone: in.Timezone}

	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		patch.DisplayName = &name
	}

	if in.BirthDate != nil {
		d, err := entity.ParseBirthDate(*in.BirthDate, now.In(uc.cfg.Location))
		if err != nil {
			return usecase.Profile{}, err
		}

		patch.BirthDate = &d
	}

	if p := in.Preferences; p != nil {
		patch.Preferences = map[string]bool{}

		if p.SidebarCollapsed != nil {
			patch.Preferences["sidebarCollapsed"] = *p.SidebarCollapsed
		}

		if p.MatureVisible != nil {
			patch.Preferences["matureVisible"] = *p.MatureVisible
		}
	}

	user, err := uc.users.UpdateProfile(ctx, userID, patch, now)
	if err != nil {
		return usecase.Profile{}, fmt.Errorf("AuthUseCase - UpdateProfile - uc.users.UpdateProfile: %w", err)
	}

	return usecase.Profile{User: user}, nil
}

// issue creates a token unless the rate limit applies; a positive wait means nothing was sent.
func (uc *UseCase) issue(ctx context.Context, user entity.User, purpose entity.TokenPurpose, now time.Time) (time.Duration, error) {
	t, err := uc.newEmailToken(purpose, now)
	if err != nil {
		return 0, err
	}

	wait, err := uc.tokens.IssueLimited(ctx, user, t, uc.limit)
	if err != nil {
		return 0, fmt.Errorf("AuthUseCase - issue - uc.tokens.IssueLimited: %w", err)
	}

	return wait, nil
}

func (uc *UseCase) newEmailToken(purpose entity.TokenPurpose, now time.Time) (repo.NewEmailToken, error) {
	raw, err := randomToken(_emailTokenBytes)
	if err != nil {
		return repo.NewEmailToken{}, fmt.Errorf("AuthUseCase - newEmailToken - %w", err)
	}

	ttl := uc.cfg.EmailVerifyTTL
	if purpose == entity.TokenReset {
		ttl = uc.cfg.PasswordResetTTL
	}

	return repo.NewEmailToken{Raw: raw, Hash: hashToken(raw), Purpose: purpose, CreatedAt: now, ExpiresAt: now.Add(ttl)}, nil
}
