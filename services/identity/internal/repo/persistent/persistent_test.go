package persistent_test

// Integration tests against a migrated nxl_identity database:
//   NXL_TEST_PG_URL_IDENTITY=postgres://user:pass@localhost:5432/nxl_identity?sslmode=disable go test ./...
// Skipped when the variable is not set.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo/persistent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type repos struct {
	pg       *postgres.Postgres
	users    repo.UserRepo
	sessions repo.SessionRepo
	tokens   repo.TokenRepo
	apps     repo.ApplicationRepo
	channels repo.ChannelRepo
	seed     repo.SeedRepo
}

func setup(t *testing.T) repos {
	t.Helper()

	url := os.Getenv("NXL_TEST_PG_URL_IDENTITY")
	if url == "" {
		t.Skip("NXL_TEST_PG_URL_IDENTITY not set")
	}

	pg, err := postgres.New(url, postgres.MaxPoolSize(8))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	_, err = pg.Pool.Exec(context.Background(), `TRUNCATE users, outbox, processed_events CASCADE`)
	require.NoError(t, err)

	return repos{
		pg: pg, users: persistent.NewUserRepo(pg), sessions: persistent.NewSessionRepo(pg), tokens: persistent.NewTokenRepo(pg),
		apps: persistent.NewApplicationRepo(pg), channels: persistent.NewChannelRepo(pg), seed: persistent.NewSeedRepo(pg),
	}
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

func newToken(purpose entity.TokenPurpose, now time.Time, ttl time.Duration) repo.NewEmailToken {
	raw := uuid.NewString()

	return repo.NewEmailToken{Raw: raw, Hash: hash(raw), Purpose: purpose, CreatedAt: now, ExpiresAt: now.Add(ttl)}
}

func createUser(t *testing.T, r repos, username string, now time.Time) (entity.User, repo.NewEmailToken) {
	t.Helper()

	u := entity.User{
		ID: uuid.NewString(), Username: username, DisplayName: username, Email: username + "@example.com",
		Locale: entity.DefaultLocale, Timezone: entity.DefaultTimezone, GlobalRole: entity.RoleUser, Status: entity.UserActive,
		CreatedAt: now, UpdatedAt: now,
	}
	verify := newToken(entity.TokenVerify, now, 24*time.Hour)

	require.NoError(t, r.users.Create(context.Background(), u, "hash", verify))

	return u, verify
}

func outboxTypes(t *testing.T, r repos) []string {
	t.Helper()

	rows, err := r.pg.Pool.Query(context.Background(), `SELECT event_type FROM outbox ORDER BY created_at, id`)
	require.NoError(t, err)

	defer rows.Close()

	var out []string

	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}

	require.NoError(t, rows.Err())

	return out
}

func TestCreate_WritesTokenAndEvents(t *testing.T) { //nolint:paralleltest // shared database
	r := setup(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	u, _ := createUser(t, r, "viewer_1", now)

	_, _, err := r.users.GetByLogin(context.Background(), "VIEWER_1")
	require.NoError(t, err)

	err = r.users.Create(context.Background(), entity.User{
		ID: uuid.NewString(), Username: "Viewer_1", DisplayName: "x", Email: "other@example.com",
		Locale: "vi", Timezone: "UTC", GlobalRole: entity.RoleUser, Status: entity.UserActive, CreatedAt: now, UpdatedAt: now,
	}, "hash", newToken(entity.TokenVerify, now, time.Hour))
	require.ErrorIs(t, err, entity.ErrUsernameTaken)

	assert.Equal(t, []string{"user.registered.v1", "auth.email_requested.v1"}, outboxTypes(t, r))

	stats, err := r.tokens.Stats(context.Background(), u.ID, entity.TokenVerify, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, stats.IssuedInWindow)
	require.NotNil(t, stats.LastIssuedAt)
}

func TestLoginFailures(t *testing.T) { //nolint:paralleltest // shared database
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	u, _ := createUser(t, r, "viewer_1", now)
	p := repo.LoginFailurePolicy{MaxFailures: 10, Window: 15 * time.Minute}

	// Concurrent wrong passwords must not lose increments.
	var wg sync.WaitGroup

	for range 9 {
		wg.Go(func() {
			_, err := r.users.RecordLoginFailure(ctx, u.ID, now, p)
			assert.NoError(t, err)
		})
	}

	wg.Wait()

	got, err := r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, 9, got.FailedLogins)
	assert.Equal(t, entity.UserActive, got.Status)

	locked, err := r.users.RecordLoginFailure(ctx, u.ID, now.Add(time.Second), p)
	require.NoError(t, err)
	assert.Equal(t, entity.UserLocked, locked.Status)
	require.NotNil(t, locked.LockedUntil)
	assert.WithinDuration(t, now.Add(time.Second+15*time.Minute), *locked.LockedUntil, time.Millisecond)

	_, err = r.users.RecordLoginFailure(ctx, u.ID, now.Add(time.Minute), p)
	require.ErrorIs(t, err, entity.ErrUserNotFound, "a locked account is not counted")

	// After the lock expired, the window restarted too.
	after, err := r.users.RecordLoginFailure(ctx, u.ID, now.Add(20*time.Minute), p)
	require.NoError(t, err)
	assert.Equal(t, entity.UserActive, after.Status)
	assert.Equal(t, 1, after.FailedLogins)

	require.NoError(t, r.users.ResetLoginFailures(ctx, u.ID, now.Add(21*time.Minute)))

	got, err = r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Zero(t, got.FailedLogins)
	assert.Nil(t, got.LastFailedLoginAt)
}

func TestTokens(t *testing.T) { //nolint:paralleltest // shared database
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	u, first := createUser(t, r, "viewer_1", now)

	// Issuing a new VERIFY token supersedes the first one.
	second := newToken(entity.TokenVerify, now.Add(time.Minute), 24*time.Hour)
	require.NoError(t, r.tokens.Issue(ctx, u, second))

	_, err := r.tokens.VerifyEmail(ctx, first.Hash, now.Add(2*time.Minute))
	require.ErrorIs(t, err, entity.ErrTokenInvalid)

	_, err = r.tokens.VerifyEmail(ctx, second.Hash, now.Add(25*time.Hour))
	require.ErrorIs(t, err, entity.ErrTokenInvalid, "expired")

	userID, err := r.tokens.VerifyEmail(ctx, second.Hash, now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, u.ID, userID)

	_, err = r.tokens.VerifyEmail(ctx, second.Hash, now.Add(3*time.Minute))
	require.ErrorIs(t, err, entity.ErrTokenInvalid, "one-time")

	got, err := r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, got.IsEmailVerified())

	// A RESET token deletes every session and lifts a temporary lock.
	require.NoError(t, r.sessions.Create(ctx, entity.Session{
		ID: uuid.NewString(), UserID: u.ID, RefreshTokenHash: hash("rt"), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}))

	for range 10 {
		_, err = r.users.RecordLoginFailure(ctx, u.ID, now, repo.LoginFailurePolicy{MaxFailures: 10, Window: 15 * time.Minute})
		require.NoError(t, err)
	}

	reset := newToken(entity.TokenReset, now, 30*time.Minute)
	require.NoError(t, r.tokens.Issue(ctx, got, reset))

	_, err = r.tokens.ResetPassword(ctx, second.Hash, "new-hash", now)
	require.ErrorIs(t, err, entity.ErrTokenInvalid, "wrong purpose")

	_, err = r.tokens.ResetPassword(ctx, reset.Hash, "new-hash", now.Add(time.Minute))
	require.NoError(t, err)

	_, err = r.sessions.GetByTokenHash(ctx, hash("rt"))
	require.ErrorIs(t, err, entity.ErrSessionNotFound)

	got, err = r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.UserActive, got.Status)
	assert.Zero(t, got.FailedLogins)

	h, err := r.users.PasswordHash(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "new-hash", h)

	assert.Equal(t, []string{
		"user.registered.v1", "auth.email_requested.v1", "auth.email_requested.v1",
		"user.email_verified.v1", "auth.email_requested.v1",
	}, outboxTypes(t, r))
}

func TestApplications(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	u, _ := createUser(t, r, "viewer_1", now)
	admin1, _ := createUser(t, r, "admin_1", now)
	admin2, _ := createUser(t, r, "admin_2", now)
	birth := time.Date(2000, 1, 31, 0, 0, 0, 0, time.UTC)

	none, err := r.apps.Latest(ctx, u.ID)
	require.NoError(t, err)
	assert.Nil(t, none)

	a := entity.Application{ID: uuid.NewString(), UserID: u.ID, LegalName: "Nguyen Van A", BirthDate: birth, CreatedAt: now}
	require.NoError(t, r.apps.Create(ctx, a))

	err = r.apps.Create(ctx, entity.Application{ID: uuid.NewString(), UserID: u.ID, LegalName: "x", BirthDate: birth, CreatedAt: now})
	require.ErrorIs(t, err, entity.ErrApplicationPending)

	state, err := r.apps.CreatorState(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, repo.CreatorState{HasPending: true}, state)

	page, err := r.apps.List(ctx, entity.ApplicationPending, nil, 10)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.NotNil(t, page[0].Applicant)
	assert.Equal(t, "viewer_1", page[0].Applicant.Username)

	// Two admins approve at once: exactly one wins.
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, adminID := range []string{admin1.ID, admin2.ID} {
		wg.Go(func() {
			e := r.apps.Approve(ctx, adminID, a.ID, time.Now().UTC())

			mu.Lock()

			errs = append(errs, e)
			mu.Unlock()
		})
	}

	wg.Wait()

	var won, lost int

	for _, e := range errs {
		switch {
		case e == nil:
			won++
		case assert.ErrorIs(t, e, entity.ErrInvalidState):
			lost++
		}
	}

	assert.Equal(t, 1, won)
	assert.Equal(t, 1, lost)

	got, err := r.apps.Get(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.ApplicationApproved, got.Status)
	require.NotNil(t, got.ReviewedBy)

	user, err := r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, user.BirthDate)
	assert.Equal(t, "2000-01-31", user.BirthDate.Format(entity.DateLayout))

	require.ErrorIs(t, r.apps.Reject(ctx, admin1.ID, a.ID, "too late for this", now), entity.ErrInvalidState)
	require.ErrorIs(t, r.apps.Approve(ctx, admin1.ID, uuid.NewString(), now), entity.ErrApplicationNotFound)

	// Cancel: only the owner's PENDING application.
	b := entity.Application{ID: uuid.NewString(), UserID: admin1.ID, LegalName: "Admin One", BirthDate: birth, CreatedAt: now.Add(time.Second)}
	require.NoError(t, r.apps.Create(ctx, b))
	require.ErrorIs(t, r.apps.Cancel(ctx, u.ID, b.ID, now), entity.ErrApplicationNotFound)
	require.NoError(t, r.apps.Cancel(ctx, admin1.ID, b.ID, now))
	require.ErrorIs(t, r.apps.Cancel(ctx, admin1.ID, b.ID, now), entity.ErrInvalidState)

	// Keyset paging over every status, oldest first.
	all, err := r.apps.List(ctx, "", nil, 1)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, a.ID, all[0].ID)

	next, err := r.apps.List(ctx, "", &repo.ApplicationCursor{CreatedAt: all[0].CreatedAt, ID: all[0].ID}, 10)
	require.NoError(t, err)
	require.Len(t, next, 1)
	assert.Equal(t, b.ID, next[0].ID)

	approved := 0

	for _, typ := range outboxTypes(t, r) {
		if typ == "creator.approved.v1" {
			approved++
		}
	}

	assert.Equal(t, 1, approved)
}

func TestUpsertChannel(t *testing.T) { //nolint:paralleltest // shared database
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	u, _ := createUser(t, r, "viewer_1", now)
	ev := uuid.NewString()
	ch := entity.UserChannel{UserID: u.ID, ChannelID: uuid.NewString(), Handle: "viewer_1", DisplayName: "Viewer"}

	require.NoError(t, r.channels.UpsertChannel(ctx, ev, ch))
	require.NoError(t, r.channels.UpsertChannel(ctx, ev, entity.UserChannel{
		UserID: u.ID, ChannelID: uuid.NewString(), Handle: "other", DisplayName: "Other",
	}), "redelivery is a no-op")

	got, err := r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ChannelID)
	assert.Equal(t, ch.ChannelID, *got.ChannelID)

	err = r.channels.UpsertChannel(ctx, uuid.NewString(), entity.UserChannel{
		UserID: uuid.NewString(), ChannelID: uuid.NewString(), Handle: "ghost", DisplayName: "Ghost",
	})
	require.ErrorIs(t, err, entity.ErrUserNotFound)

	other, _ := createUser(t, r, "viewer_2", now)
	err = r.channels.UpsertChannel(ctx, uuid.NewString(), entity.UserChannel{
		UserID: other.ID, ChannelID: ch.ChannelID, Handle: "viewer_2", DisplayName: "Other",
	})
	require.ErrorIs(t, err, entity.ErrChannelTaken)

	state, err := r.apps.CreatorState(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, state.HasChannel)
}

func TestUpdateProfile(t *testing.T) { //nolint:paralleltest // shared database
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	u, _ := createUser(t, r, "viewer_1", now)
	name, birth := "Minh", time.Date(2000, 1, 31, 0, 0, 0, 0, time.UTC)

	got, err := r.users.UpdateProfile(ctx, u.ID, repo.ProfilePatch{
		DisplayName: &name, BirthDate: &birth, Preferences: map[string]bool{"matureVisible": true},
	}, now)
	require.NoError(t, err)
	assert.Equal(t, "Minh", got.DisplayName)
	assert.True(t, got.Preferences.MatureVisible)

	got, err = r.users.UpdateProfile(ctx, u.ID, repo.ProfilePatch{Preferences: map[string]bool{"sidebarCollapsed": true}}, now)
	require.NoError(t, err)
	assert.Equal(t, entity.Preferences{SidebarCollapsed: true, MatureVisible: true}, got.Preferences, "merged")

	_, err = r.users.UpdateProfile(ctx, u.ID, repo.ProfilePatch{BirthDate: &birth}, now)
	require.ErrorIs(t, err, entity.ErrBirthDateAlreadySet)

	assert.Contains(t, outboxTypes(t, r), "user.profile_updated.v1")
}

func TestSeedAndPromote(t *testing.T) { //nolint:paralleltest // shared database
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	admin := entity.User{
		ID: uuid.NewString(), Username: "admin", DisplayName: "Admin", Email: "admin@nexuslive.dev", EmailVerifiedAt: &now,
		Locale: "vi", Timezone: "Asia/Ho_Chi_Minh", GlobalRole: entity.RoleAdmin, Status: entity.UserActive, CreatedAt: now, UpdatedAt: now,
	}

	stored, created, err := r.seed.EnsureUser(ctx, admin, "hash")
	require.NoError(t, err)
	assert.True(t, created)

	again := admin
	again.ID = uuid.NewString()

	stored2, created, err := r.seed.EnsureUser(ctx, again, "hash")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, stored.ID, stored2.ID)
	assert.Equal(t, []string{"user.registered.v1", "user.email_verified.v1"}, outboxTypes(t, r), "announced once")

	creator, _ := createUser(t, r, "creator", now)
	app := entity.Application{ID: uuid.NewString(), UserID: creator.ID, LegalName: "Creator", BirthDate: now, CreatedAt: now}

	ok, err := r.seed.EnsureApproved(ctx, app, stored.ID)
	require.NoError(t, err)
	assert.True(t, ok)

	app.ID = uuid.NewString()
	ok, err = r.seed.EnsureApproved(ctx, app, stored.ID)
	require.NoError(t, err)
	assert.False(t, ok, "idempotent")

	promoted, err := r.users.PromoteAdmin(ctx, "CREATOR@example.com")
	require.NoError(t, err)
	assert.Equal(t, entity.RoleAdmin, promoted.GlobalRole)

	_, err = r.users.PromoteAdmin(ctx, "ghost@example.com")
	require.ErrorIs(t, err, entity.ErrUserNotFound)
}

func TestIssueLimited_Concurrent(t *testing.T) { //nolint:paralleltest // shared database
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	// Registration issued a VERIFY token now; the reset limit is independent of it.
	u, _ := createUser(t, r, "viewer_1", now)
	limit := entity.TokenLimit{MinInterval: time.Minute, MaxInWindow: 5, Window: time.Hour}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		issued int
	)

	for i := range 8 {
		wg.Go(func() {
			wait, err := r.tokens.IssueLimited(ctx, u, newToken(entity.TokenReset, now.Add(time.Duration(i)*time.Millisecond), time.Hour), limit)
			assert.NoError(t, err)

			if wait == 0 {
				mu.Lock()
				issued++
				mu.Unlock()
			}
		})
	}

	wg.Wait()
	assert.Equal(t, 1, issued, "only one of the concurrent requests may issue a token")

	stats, err := r.tokens.Stats(ctx, u.ID, entity.TokenReset, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, stats.IssuedInWindow)

	wait, err := r.tokens.IssueLimited(ctx, u, newToken(entity.TokenReset, now.Add(2*time.Minute), time.Hour), limit)
	require.NoError(t, err)
	assert.Zero(t, wait, "allowed again after the minimum interval")
}
