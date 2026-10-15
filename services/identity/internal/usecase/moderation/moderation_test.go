package moderation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase/moderation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

type fakeBlocks struct {
	repo.BlockRepo

	addErr error
	added  bool
	rows   []entity.Block
	max    int
}

func (f *fakeBlocks) Add(_ context.Context, _, _ string, maxBlocks int) (bool, error) {
	f.max = maxBlocks

	return f.added, f.addErr
}

func (f *fakeBlocks) List(_ context.Context, _ string, _ *repo.BlockCursor, limit int) ([]entity.Block, error) {
	if len(f.rows) > limit {
		return f.rows[:limit], nil
	}

	return f.rows, nil
}

type fakeUsers struct {
	repo.UserRepo

	u   entity.User
	err error
}

func (f *fakeUsers) GetByID(context.Context, string) (entity.User, error) { return f.u, f.err }

type fakeActions struct {
	changed bool
	err     error
}

func (f *fakeActions) ApplyUserAction(context.Context, string, events.UserActioned) (bool, error) {
	return f.changed, f.err
}

func TestBlock(t *testing.T) {
	t.Parallel()

	me, other := uuid.NewString(), uuid.NewString()

	tests := []struct {
		name   string
		target string
		addErr error
		want   error
	}{
		{name: "ok", target: other},
		{name: "yourself", target: me, want: entity.ErrSelfBlock},
		{name: "unknown user", target: other, addErr: entity.ErrNotFound, want: entity.ErrNotFound},
		{name: "1000 already", target: other, addErr: entity.ErrBlockLimit, want: entity.ErrBlockLimit},
		{name: "db", target: other, addErr: errDB, want: errDB},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			blocks := &fakeBlocks{added: true, addErr: tc.addErr}
			uc := moderation.New(moderation.Repos{Blocks: blocks}, logger.New("error"))

			err := uc.Block(context.Background(), me, tc.target)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, entity.BlockMax, blocks.max)
		})
	}
}

func TestListCursor(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	rows := []entity.Block{
		{UserID: uuid.NewString(), CreatedAt: now},
		{UserID: uuid.NewString(), CreatedAt: now.Add(-time.Second)},
		{UserID: uuid.NewString(), CreatedAt: now.Add(-2 * time.Second)},
	}
	uc := moderation.New(moderation.Repos{Blocks: &fakeBlocks{rows: rows}}, logger.New("error"))

	page, err := uc.List(context.Background(), "u", "", 2)
	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
	require.NotEmpty(t, page.NextCursor)

	_, err = uc.List(context.Background(), "u", page.NextCursor, 2)
	require.NoError(t, err)

	_, err = uc.List(context.Background(), "u", "not-a-cursor", 2)
	require.ErrorIs(t, err, entity.ErrInvalidCursor)
}

func TestApply(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		repo *fakeActions
		ok   bool
	}{
		{name: "changed", repo: &fakeActions{changed: true}, ok: true},
		{name: "nothing to change is acked", repo: &fakeActions{}, ok: true},
		{name: "db error is retried", repo: &fakeActions{err: errDB}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc := moderation.New(moderation.Repos{Actions: tc.repo}, logger.New("error"))
			err := uc.Apply(context.Background(), uuid.NewString(), events.UserActioned{Action: events.ActionLock})

			if tc.ok {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errDB)
			}
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	now := time.Now()
	past := now.Add(-time.Hour)
	kind := entity.LockKindAdmin

	uc := moderation.New(moderation.Repos{Users: &fakeUsers{u: entity.User{
		ID: "u", Status: entity.UserLocked, LockedUntil: &past, LockKind: &kind,
	}}}, logger.New("error")).WithClock(func() time.Time { return now })

	u, err := uc.Get(context.Background(), "u")
	require.NoError(t, err)
	assert.Equal(t, entity.UserActive, u.Status, "an expired lock reads as ACTIVE")
	assert.Nil(t, u.LockKind)

	missing := moderation.New(moderation.Repos{Users: &fakeUsers{err: entity.ErrUserNotFound}}, logger.New("error"))
	_, err = missing.Get(context.Background(), "u")
	require.ErrorIs(t, err, entity.ErrNotFound)
}

func TestSnapshotCursor(t *testing.T) {
	t.Parallel()

	uc := moderation.New(moderation.Repos{}, logger.New("error"))

	_, _, err := uc.Users(context.Background(), "x", 10)
	require.ErrorIs(t, err, entity.ErrInvalidCursor)

	_, _, err = uc.Blocks(context.Background(), "a:b", 10)
	require.ErrorIs(t, err, entity.ErrInvalidCursor)
}
