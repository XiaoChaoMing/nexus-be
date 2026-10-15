package persistent_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo/persistent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlocks(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a, _ := createUser(t, r, "viewer_a", now)
	b, _ := createUser(t, r, "viewer_b", now)
	c, _ := createUser(t, r, "viewer_c", now)
	blocks := persistent.NewBlockRepo(r.pg)

	added, err := blocks.Add(ctx, a.ID, b.ID, 2)
	require.NoError(t, err)
	assert.True(t, added)

	added, err = blocks.Add(ctx, a.ID, b.ID, 2)
	require.NoError(t, err)
	assert.False(t, added, "idempotent")

	_, err = blocks.Add(ctx, a.ID, uuid.NewString(), 2)
	require.ErrorIs(t, err, entity.ErrNotFound)

	added, err = blocks.Add(ctx, a.ID, c.ID, 2)
	require.NoError(t, err)
	assert.True(t, added)

	d, _ := createUser(t, r, "viewer_d", now)
	_, err = blocks.Add(ctx, a.ID, d.ID, 2)
	require.ErrorIs(t, err, entity.ErrBlockLimit)

	list, err := blocks.List(ctx, a.ID, nil, 1)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, c.ID, list[0].UserID, "newest first")

	next, err := blocks.List(ctx, a.ID, &repo.BlockCursor{CreatedAt: list[0].CreatedAt, UserID: list[0].UserID}, 5)
	require.NoError(t, err)
	require.Len(t, next, 1)
	assert.Equal(t, "viewer_b", next[0].Username)

	removed, err := blocks.Remove(ctx, a.ID, b.ID)
	require.NoError(t, err)
	assert.True(t, removed)

	removed, err = blocks.Remove(ctx, a.ID, b.ID)
	require.NoError(t, err)
	assert.False(t, removed)

	types := outboxTypes(t, r)
	assert.Equal(t, 2, count(types, events.TypeUserBlocked))
	assert.Equal(t, 1, count(types, events.TypeUserUnblocked))

	snap := persistent.NewModerationRepo(r.pg)
	pairs, err := snap.Blocks(ctx, nil, 10)
	require.NoError(t, err)
	assert.Equal(t, []entity.BlockPair{{BlockerID: a.ID, BlockedID: c.ID}}, pairs)
}

func TestApplyUserAction(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	u, _ := createUser(t, r, "viewer_1", now)
	mod := persistent.NewModerationRepo(r.pg)

	require.NoError(t, r.sessions.Create(ctx, entity.Session{
		ID: uuid.NewString(), UserID: u.ID, RefreshTokenHash: hash("rt1"), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}))

	until := now.Add(24 * time.Hour).Truncate(time.Microsecond)
	lock := events.UserActioned{ActionID: uuid.NewString(), UserID: u.ID, Action: events.ActionLock, LockedUntil: &until, Reason: "spam in chat"}
	eventID := uuid.NewString()

	changed, err := mod.ApplyUserAction(ctx, eventID, lock)
	require.NoError(t, err)
	assert.True(t, changed)

	changed, err = mod.ApplyUserAction(ctx, eventID, lock)
	require.NoError(t, err)
	assert.False(t, changed, "redelivery")

	got, err := r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.UserLocked, got.Status)
	require.NotNil(t, got.LockKind)
	assert.Equal(t, entity.LockKindAdmin, *got.LockKind)
	assert.WithinDuration(t, until, *got.LockedUntil, time.Millisecond)

	_, err = r.sessions.GetByTokenHash(ctx, hash("rt1"))
	require.ErrorIs(t, err, entity.ErrSessionNotFound, "sessions ended")

	// A password reset does not lift an admin lock.
	reset := newToken(entity.TokenReset, now, 30*time.Minute)
	require.NoError(t, r.tokens.Issue(ctx, got, reset))
	_, err = r.tokens.ResetPassword(ctx, reset.Hash, "new-hash", now)
	require.NoError(t, err)

	got, err = r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.UserLocked, got.Status)

	// A failed sign-in does not replace it either.
	_, err = r.users.RecordLoginFailure(ctx, u.ID, now, repo.LoginFailurePolicy{MaxFailures: 1, Window: time.Minute})
	require.ErrorIs(t, err, entity.ErrUserNotFound)

	unlock := events.UserActioned{ActionID: uuid.NewString(), UserID: u.ID, Action: events.ActionUnlock, Reason: "appeal accepted"}
	changed, err = mod.ApplyUserAction(ctx, uuid.NewString(), unlock)
	require.NoError(t, err)
	assert.True(t, changed)

	changed, err = mod.ApplyUserAction(ctx, uuid.NewString(), unlock)
	require.NoError(t, err)
	assert.False(t, changed, "not locked")

	got, err = r.users.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.UserActive, got.Status)
	assert.Nil(t, got.LockKind)

	// Admins cannot be locked.
	admin, _ := createUser(t, r, "admin_1", now)
	_, err = r.pg.Pool.Exec(ctx, `UPDATE users SET global_role = 'ADMIN' WHERE id = $1`, admin.ID)
	require.NoError(t, err)

	lock.UserID = admin.ID
	changed, err = mod.ApplyUserAction(ctx, uuid.NewString(), lock)
	require.NoError(t, err)
	assert.False(t, changed)

	types := outboxTypes(t, r)
	assert.Equal(t, 1, count(types, events.TypeUserLocked))
	assert.Equal(t, 1, count(types, events.TypeUserUnlocked))

	users, err := mod.Users(ctx, "", 1)
	require.NoError(t, err)
	require.Len(t, users, 1)

	rest, err := mod.Users(ctx, users[0].ID, 10)
	require.NoError(t, err)
	assert.Len(t, rest, 1)
}

func TestUpdateProfile_LocaleEvent(t *testing.T) { //nolint:paralleltest // shared database
	r := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	u, _ := createUser(t, r, "viewer_1", now)
	en := "en"

	_, err := r.users.UpdateProfile(ctx, u.ID, repo.ProfilePatch{Locale: &en}, now)
	require.NoError(t, err)

	_, err = r.users.UpdateProfile(ctx, u.ID, repo.ProfilePatch{Locale: &en}, now)
	require.NoError(t, err)

	assert.Equal(t, 1, count(outboxTypes(t, r), events.TypeUserProfileUpdated), "only a real change is announced")
}

func count(list []string, v string) int {
	n := 0

	for _, s := range list {
		if s == v {
			n++
		}
	}

	return n
}
