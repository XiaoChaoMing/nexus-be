package channels_test

import (
	"context"
	"errors"
	"testing"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/channels"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

// fakeRepo keeps channels in memory and mimics the handle and owner uniqueness of the table.
type fakeRepo struct {
	byOwner   map[string]entity.Channel
	processed map[string]bool
	err       error
	live      map[string]string
}

func newFake() *fakeRepo {
	return &fakeRepo{byOwner: map[string]entity.Channel{}, processed: map[string]bool{}, live: map[string]string{}}
}

func (f *fakeRepo) ByHandle(_ context.Context, handle string) (entity.Channel, error) {
	if f.err != nil {
		return entity.Channel{}, f.err
	}

	for _, c := range f.byOwner {
		if c.Handle == handle {
			return c, nil
		}
	}

	return entity.Channel{}, entity.ErrNotFound
}

func (f *fakeRepo) taken(handle string) bool {
	for _, c := range f.byOwner {
		if c.Handle == handle {
			return true
		}
	}

	return false
}

func (f *fakeRepo) CreateForOwner(_ context.Context, eventID string, in repo.NewChannel) (entity.Channel, bool, error) {
	if f.err != nil {
		return entity.Channel{}, false, f.err
	}

	if f.processed[eventID] {
		return entity.Channel{}, false, nil
	}

	f.processed[eventID] = true

	if c, ok := f.byOwner[in.OwnerID]; ok {
		return c, false, nil
	}

	for n := 1; ; n++ {
		if h := entity.HandleCandidate(in.BaseHandle, n); !f.taken(h) {
			c := entity.Channel{ID: "ch-" + in.OwnerID, OwnerID: in.OwnerID, Handle: h, DisplayName: in.DisplayName, Status: entity.StatusActive}
			f.byOwner[in.OwnerID] = c

			return c, true, nil
		}
	}
}

func (f *fakeRepo) SetLive(_ context.Context, _, channelID, streamID string) error {
	f.live[channelID] = streamID

	return f.err
}

func (f *fakeRepo) SetOffline(_ context.Context, _, channelID, _ string) error {
	delete(f.live, channelID)

	return f.err
}

func TestCreatorApprovedIdempotentAndSuffixed(t *testing.T) {
	t.Parallel()

	f := newFake()
	uc := channels.New(f, logger.New("error"))
	ctx := context.Background()

	require.NoError(t, uc.CreatorApproved(ctx, "e1", usecase.CreatorApproved{UserID: "u1", Username: "MinhNN", DisplayName: "Minh"}))
	// Redelivery of the same event and a second approval of the same owner create nothing.
	require.NoError(t, uc.CreatorApproved(ctx, "e1", usecase.CreatorApproved{UserID: "u1", Username: "MinhNN"}))
	require.NoError(t, uc.CreatorApproved(ctx, "e2", usecase.CreatorApproved{UserID: "u1", Username: "other"}))
	require.Len(t, f.byOwner, 1)
	require.Equal(t, "minhnn", f.byOwner["u1"].Handle)
	require.Equal(t, "Minh", f.byOwner["u1"].DisplayName)

	// Another owner whose username maps to the same handle gets a numeric suffix.
	require.NoError(t, uc.CreatorApproved(ctx, "e3", usecase.CreatorApproved{UserID: "u2", Username: "minhnn"}))
	require.NoError(t, uc.CreatorApproved(ctx, "e4", usecase.CreatorApproved{UserID: "u3", Username: "MINHNN"}))
	require.Equal(t, "minhnn2", f.byOwner["u2"].Handle)
	require.Equal(t, "minhnn", f.byOwner["u2"].DisplayName, "falls back to the username")
	require.Equal(t, "minhnn3", f.byOwner["u3"].Handle)
}

func TestCreatorApprovedRepoError(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.err = errDB

	err := channels.New(f, logger.New("error")).CreatorApproved(context.Background(), "e1",
		usecase.CreatorApproved{UserID: "u1", Username: "minhnn"})
	require.ErrorIs(t, err, errDB)
}

func TestByHandle(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.byOwner["u1"] = entity.Channel{ID: "c1", Handle: "minhnn", Status: entity.StatusActive}
	f.byOwner["u2"] = entity.Channel{ID: "c2", Handle: "banned", Status: entity.StatusSuspended}
	uc := channels.New(f, logger.New("error"))

	tests := []struct {
		name, handle string
		wantID       string
		wantErr      error
	}{
		{name: "found", handle: "minhnn", wantID: "c1"},
		{name: "unknown", handle: "nobody", wantErr: entity.ErrNotFound},
		{name: "malformed", handle: "a-b", wantErr: entity.ErrNotFound},
		{name: "too short", handle: "ab", wantErr: entity.ErrNotFound},
		{name: "suspended", handle: "banned", wantErr: entity.ErrSuspended},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ch, err := uc.ByHandle(context.Background(), tc.handle)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.wantID, ch.ID)
		})
	}
}

func TestByHandleRepoError(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.err = errDB

	_, err := channels.New(f, logger.New("error")).ByHandle(context.Background(), "minhnn")
	require.ErrorIs(t, err, errDB)
}

func TestLiveState(t *testing.T) {
	t.Parallel()

	f := newFake()
	uc := channels.New(f, logger.New("error"))
	ctx := context.Background()

	require.NoError(t, uc.StreamStarted(ctx, "e1", "c1", "s1"))
	require.Equal(t, "s1", f.live["c1"])
	require.NoError(t, uc.StreamEnded(ctx, "e2", "c1", "s1"))
	require.Empty(t, f.live)

	f.err = errDB
	require.ErrorIs(t, uc.StreamStarted(ctx, "e3", "c1", "s2"), errDB)
	require.ErrorIs(t, uc.StreamEnded(ctx, "e4", "c1", "s2"), errDB)
}
