// Package bootstrap creates the development accounts and promotes admins from the command line.
package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
)

// Development accounts; they only exist when NXL_ENV=dev and IDENTITY_DEV_SEED=true.
const (
	_adminUsername   = "admin"
	_adminEmail      = "admin@nexuslive.dev"
	_creatorUsername = "creator"
	_creatorEmail    = "creator@nexuslive.dev"
	_creatorName     = "NexusLive Creator"
	_creatorBirth    = "2000-01-01"
)

// PasswordHasher -.
type PasswordHasher interface {
	Hash(password string) (string, error)
}

// UseCase -.
type UseCase struct {
	seed   repo.SeedRepo
	users  repo.UserRepo
	hasher PasswordHasher
	now    func() time.Time
	l      logger.Interface
}

// New -.
func New(seed repo.SeedRepo, users repo.UserRepo, hasher PasswordHasher, l logger.Interface) *UseCase {
	return &UseCase{seed: seed, users: users, hasher: hasher, now: func() time.Time { return time.Now().UTC() }, l: l}
}

// SeedDev creates, when missing, an admin and an approved creator whose creator.approved event makes
// channel-svc create the demo channel. Safe to run at every start.
func (uc *UseCase) SeedDev(ctx context.Context, password string) error {
	hash, err := uc.hasher.Hash(password)
	if err != nil {
		return fmt.Errorf("BootstrapUseCase - SeedDev - hasher.Hash: %w", err)
	}

	admin, err := uc.ensure(ctx, _adminUsername, _adminEmail, "Admin", entity.RoleAdmin, hash)
	if err != nil {
		return err
	}

	creator, err := uc.ensure(ctx, _creatorUsername, _creatorEmail, _creatorName, entity.RoleUser, hash)
	if err != nil {
		return err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("BootstrapUseCase - SeedDev - uuid.NewV7: %w", err)
	}

	birth, err := time.Parse(entity.DateLayout, _creatorBirth)
	if err != nil {
		return fmt.Errorf("BootstrapUseCase - SeedDev - birth date: %w", err)
	}

	created, err := uc.seed.EnsureApproved(ctx, entity.Application{
		ID: id.String(), UserID: creator.ID, LegalName: _creatorName, BirthDate: birth, CreatedAt: uc.now(),
	}, admin.ID)
	if err != nil {
		return fmt.Errorf("BootstrapUseCase - SeedDev - seed.EnsureApproved: %w", err)
	}

	if created {
		uc.l.Info("bootstrap - dev seed: creator application approved for %s", _creatorUsername)
	}

	return nil
}

func (uc *UseCase) ensure(ctx context.Context, username, email, name, role, hash string) (entity.User, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return entity.User{}, fmt.Errorf("BootstrapUseCase - ensure - uuid.NewV7: %w", err)
	}

	now := uc.now()

	u, created, err := uc.seed.EnsureUser(ctx, entity.User{
		ID: id.String(), Username: username, DisplayName: name, Email: email, EmailVerifiedAt: &now,
		Locale: entity.DefaultLocale, Timezone: entity.DefaultTimezone, GlobalRole: role, Status: entity.UserActive,
		CreatedAt: now, UpdatedAt: now,
	}, hash)
	if err != nil {
		return entity.User{}, fmt.Errorf("BootstrapUseCase - ensure %s - seed.EnsureUser: %w", username, err)
	}

	if created {
		uc.l.Info("bootstrap - dev seed: created %s (%s)", username, role)
	} else {
		uc.l.Info("bootstrap - dev seed: %s already exists", username)
	}

	return u, nil
}

// PromoteAdmin gives the account with this e-mail the ADMIN role; entity.ErrUserNotFound when there is none.
// The role reaches access tokens at the next refresh.
func (uc *UseCase) PromoteAdmin(ctx context.Context, email string) (entity.User, error) {
	u, err := uc.users.PromoteAdmin(ctx, email)
	if err != nil {
		return entity.User{}, fmt.Errorf("BootstrapUseCase - PromoteAdmin - users.PromoteAdmin: %w", err)
	}

	uc.l.Info("audit=admin_promoted userID=%s", u.ID)

	return u, nil
}
