package bootstrap_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase/bootstrap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

type approval struct {
	app     entity.Application
	adminID string
}

// fakeSeed keeps users by username and the approved applications, like the SQL "ensure" semantics.
type fakeSeed struct {
	users       map[string]entity.User
	hashes      map[string]string
	approved    []approval
	userErr     error
	approvedErr error
}

func newFakeSeed() *fakeSeed {
	return &fakeSeed{users: map[string]entity.User{}, hashes: map[string]string{}}
}

func (f *fakeSeed) EnsureUser(_ context.Context, u entity.User, hash string) (entity.User, bool, error) {
	if f.userErr != nil {
		return entity.User{}, false, f.userErr
	}

	if got, ok := f.users[u.Username]; ok {
		return got, false, nil
	}

	f.users[u.Username] = u
	f.hashes[u.Username] = hash

	return u, true, nil
}

func (f *fakeSeed) EnsureApproved(_ context.Context, a entity.Application, adminID string) (bool, error) {
	if f.approvedErr != nil {
		return false, f.approvedErr
	}

	for _, x := range f.approved {
		if x.app.UserID == a.UserID {
			return false, nil
		}
	}

	f.approved = append(f.approved, approval{app: a, adminID: adminID})

	return true, nil
}

// fakeUsers only implements PromoteAdmin; any other call panics through the nil embedded interface.
type fakeUsers struct {
	repo.UserRepo

	byEmail map[string]entity.User
}

func (f fakeUsers) PromoteAdmin(_ context.Context, email string) (entity.User, error) {
	u, ok := f.byEmail[email]
	if !ok {
		return entity.User{}, entity.ErrUserNotFound
	}

	u.GlobalRole = entity.RoleAdmin

	return u, nil
}

type fakeHasher struct{ err error }

func (h fakeHasher) Hash(p string) (string, error) {
	if h.err != nil {
		return "", h.err
	}

	return "hash:" + p, nil
}

func newUseCase(seed *fakeSeed, h fakeHasher) *bootstrap.UseCase {
	return bootstrap.New(seed, fakeUsers{byEmail: map[string]entity.User{
		"viewer@example.com": {ID: "u-1", Email: "viewer@example.com", GlobalRole: entity.RoleUser},
	}}, h, logger.New("error"))
}

func TestSeedDev_CreatesAccountsOnce(t *testing.T) {
	t.Parallel()

	seed := newFakeSeed()
	uc := newUseCase(seed, fakeHasher{})

	require.NoError(t, uc.SeedDev(context.Background(), "nexuslive123"))

	admin, creator := seed.users["admin"], seed.users["creator"]
	assert.Equal(t, "admin@nexuslive.dev", admin.Email)
	assert.Equal(t, entity.RoleAdmin, admin.GlobalRole)
	assert.Equal(t, "creator@nexuslive.dev", creator.Email)
	assert.Equal(t, entity.RoleUser, creator.GlobalRole)

	for _, u := range []entity.User{admin, creator} {
		assert.True(t, u.IsEmailVerified(), u.Username)
		assert.Equal(t, entity.UserActive, u.Status, u.Username)
		assert.Equal(t, "hash:nexuslive123", seed.hashes[u.Username], "the password is stored hashed")
	}

	require.Len(t, seed.approved, 1)
	got := seed.approved[0]
	assert.Equal(t, creator.ID, got.app.UserID)
	assert.Equal(t, admin.ID, got.adminID, "the seed admin reviews the seed application")
	assert.Equal(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), got.app.BirthDate)
	assert.NotEmpty(t, got.app.ID)

	// A second start changes nothing.
	require.NoError(t, uc.SeedDev(context.Background(), "nexuslive123"))
	assert.Len(t, seed.users, 2)
	assert.Len(t, seed.approved, 1)
	assert.Equal(t, admin.ID, seed.users["admin"].ID)
}

func TestSeedDev_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		hasher fakeHasher
		seed   func(*fakeSeed)
	}{
		{name: "hasher fails", hasher: fakeHasher{err: errDB}, seed: func(*fakeSeed) {}},
		{name: "user insert fails", seed: func(s *fakeSeed) { s.userErr = errDB }},
		{name: "approval fails", seed: func(s *fakeSeed) { s.approvedErr = errDB }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			seed := newFakeSeed()
			tc.seed(seed)

			err := newUseCase(seed, tc.hasher).SeedDev(context.Background(), "nexuslive123")
			require.ErrorIs(t, err, errDB)
		})
	}
}

func TestPromoteAdmin(t *testing.T) {
	t.Parallel()

	uc := newUseCase(newFakeSeed(), fakeHasher{})

	u, err := uc.PromoteAdmin(context.Background(), "viewer@example.com")
	require.NoError(t, err)
	assert.Equal(t, entity.RoleAdmin, u.GlobalRole)

	_, err = uc.PromoteAdmin(context.Background(), "ghost@example.com")
	require.ErrorIs(t, err, entity.ErrUserNotFound)
}
