package channels_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/channels"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

// fakeRepo keeps channels and follows in memory and mimics the handle and owner uniqueness of the table.
// Methods a test does not use are left to the embedded (nil) interfaces.
type fakeRepo struct {
	repo.ScheduleRepo
	repo.ReminderRepo
	repo.SearchRepo

	byOwner   map[string]entity.Channel
	processed map[string]bool
	err       error
	live      map[string]entity.LiveStream
	follows   map[string]entity.Follow // user/channel
}

func newFake() *fakeRepo {
	return &fakeRepo{
		byOwner: map[string]entity.Channel{}, processed: map[string]bool{}, live: map[string]entity.LiveStream{},
		follows: map[string]entity.Follow{},
	}
}

func (f *fakeRepo) find(match func(entity.Channel) bool) (entity.Channel, error) {
	if f.err != nil {
		return entity.Channel{}, f.err
	}

	for _, c := range f.byOwner {
		if match(c) {
			return c, nil
		}
	}

	return entity.Channel{}, entity.ErrNotFound
}

func (f *fakeRepo) ByHandle(_ context.Context, handle string) (entity.Channel, error) {
	return f.find(func(c entity.Channel) bool { return c.Handle == handle })
}

func (f *fakeRepo) ByID(_ context.Context, id string) (entity.Channel, error) {
	return f.find(func(c entity.Channel) bool { return c.ID == id })
}

func (f *fakeRepo) ByOwner(_ context.Context, owner string) (entity.Channel, error) {
	return f.find(func(c entity.Channel) bool { return c.OwnerID == owner })
}

func (f *fakeRepo) taken(handle string) bool {
	_, err := f.find(func(c entity.Channel) bool { return c.Handle == handle })

	return err == nil
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

func (f *fakeRepo) UpdateProfile(context.Context, string, entity.ProfilePatch, time.Time) (entity.Channel, error) {
	return entity.Channel{}, nil
}

func (f *fakeRepo) SetImage(context.Context, string, entity.ImageKind, string) (*string, error) {
	return nil, nil //nolint:nilnil // unused here
}

func (f *fakeRepo) SetLive(_ context.Context, _ string, s entity.LiveStream) error {
	f.live[s.ChannelID] = s

	return f.err
}

func (f *fakeRepo) UpdateLive(_ context.Context, _ string, s entity.LiveStream) error {
	f.live[s.ChannelID] = s

	return f.err
}

func (f *fakeRepo) SetOffline(_ context.Context, _, channelID, _ string) error {
	delete(f.live, channelID)

	return f.err
}

func (f *fakeRepo) LiveStreams(_ context.Context, ids []string) (map[string]entity.LiveStream, error) {
	out := map[string]entity.LiveStream{}

	for _, id := range ids {
		if s, ok := f.live[id]; ok {
			out[id] = s
		}
	}

	return out, f.err
}

func (f *fakeRepo) LiveStreamIDs(context.Context) ([]string, error) { return nil, nil }

func (f *fakeRepo) Follow(_ context.Context, userID, channelID string) (entity.Follow, bool, error) {
	if fl, ok := f.follows[userID+"/"+channelID]; ok {
		return fl, false, nil
	}

	fl := entity.Follow{UserID: userID, ChannelID: channelID, NotifyLevel: entity.NotifyAll, CreatedAt: time.Now()}
	f.follows[userID+"/"+channelID] = fl

	return fl, true, nil
}

func (f *fakeRepo) SetNotifyLevel(_ context.Context, userID, channelID string, l entity.NotifyLevel) (entity.Follow, error) {
	fl, ok := f.follows[userID+"/"+channelID]
	if !ok {
		return entity.Follow{}, entity.ErrNotFollowing
	}

	fl.NotifyLevel = l
	f.follows[userID+"/"+channelID] = fl

	return fl, nil
}

func (f *fakeRepo) Unfollow(_ context.Context, userID, channelID string) error {
	delete(f.follows, userID+"/"+channelID)

	return nil
}

func (f *fakeRepo) GetFollow(_ context.Context, userID, channelID string) (*entity.Follow, error) {
	if fl, ok := f.follows[userID+"/"+channelID]; ok {
		return &fl, nil
	}

	return nil, nil //nolint:nilnil // not following
}

func (f *fakeRepo) followed(userID string, keep func(entity.Channel) bool) []entity.FollowedChannel {
	out := []entity.FollowedChannel{}

	for _, fl := range f.follows {
		if fl.UserID != userID {
			continue
		}

		if c, err := f.ByID(context.Background(), fl.ChannelID); err == nil && keep(c) {
			out = append(out, entity.FollowedChannel{Channel: c, NotifyLevel: fl.NotifyLevel, FollowedAt: fl.CreatedAt})
		}
	}

	return out
}

func (f *fakeRepo) ListLive(_ context.Context, userID string, _ int) ([]entity.FollowedChannel, error) {
	return f.followed(userID, func(c entity.Channel) bool { return c.IsLive }), nil
}

func (f *fakeRepo) ListFollowed(_ context.Context, userID string, offline bool, _ *repo.FollowCursor, limit int) ([]entity.FollowedChannel, error) {
	out := f.followed(userID, func(c entity.Channel) bool { return !offline || !c.IsLive })
	if len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

func (f *fakeRepo) CountByUser(_ context.Context, userID string) (int, error) {
	return len(f.followed(userID, func(entity.Channel) bool { return true })), nil
}

func (f *fakeRepo) NewFollowers(context.Context, string, time.Time) (int, error) { return 0, nil }

type fakeCategories struct{ repo.CategoryRepo }

func (fakeCategories) ByIDs(_ context.Context, ids []string) (map[string]entity.Category, error) {
	out := map[string]entity.Category{}
	for _, id := range ids {
		out[id] = entity.Category{ID: id, Slug: "slug-" + id, Name: "Cat " + id}
	}

	return out, nil
}

type fakeViewers map[string]int

func (v fakeViewers) ViewerCounts(_ context.Context, ids []string) (map[string]int, error) {
	out := map[string]int{}

	for _, id := range ids {
		if n, ok := v[id]; ok {
			out[id] = n
		}
	}

	return out, nil
}

func newUC(f *fakeRepo, counts fakeViewers) *channels.UseCase {
	l := logger.New("error")

	return channels.New(channels.Repos{Channels: f, Follows: f, Schedules: f, Reminders: f, Search: f},
		enrich.New(fakeCategories{}, counts, l), l)
}

func TestCreatorApprovedIdempotentAndSuffixed(t *testing.T) {
	t.Parallel()

	f := newFake()
	uc := newUC(f, nil)
	ctx := context.Background()

	require.NoError(t, uc.CreatorApproved(ctx, "e1", usecase.CreatorApproved{UserID: "u1", Username: "MinhNN", DisplayName: "Minh"}))
	// Redelivery of the same event and a second approval of the same owner create nothing.
	require.NoError(t, uc.CreatorApproved(ctx, "e1", usecase.CreatorApproved{UserID: "u1", Username: "MinhNN"}))
	require.NoError(t, uc.CreatorApproved(ctx, "e2", usecase.CreatorApproved{UserID: "u1", Username: "other"}))
	require.Len(t, f.byOwner, 1)
	require.Equal(t, "minhnn", f.byOwner["u1"].Handle)

	// Another owner whose username maps to the same handle gets a numeric suffix.
	require.NoError(t, uc.CreatorApproved(ctx, "e3", usecase.CreatorApproved{UserID: "u2", Username: "minhnn"}))
	require.Equal(t, "minhnn2", f.byOwner["u2"].Handle)
	require.Equal(t, "minhnn", f.byOwner["u2"].DisplayName, "falls back to the username")

	f.err = errDB
	require.ErrorIs(t, uc.CreatorApproved(ctx, "e9", usecase.CreatorApproved{UserID: "u9", Username: "x"}), errDB)
}

func TestProfile(t *testing.T) {
	t.Parallel()

	f := newFake()
	cat := "cat1"
	f.byOwner["u1"] = entity.Channel{ID: "c1", OwnerID: "u1", Handle: "minhnn", Status: entity.StatusActive, IsLive: true}
	avatar := "https://cdn.example/a.webp"
	f.byOwner["u2"] = entity.Channel{
		ID: "c2", OwnerID: "u2", Handle: "banned", DisplayName: "Banned", AvatarURL: &avatar, Status: entity.StatusSuspended,
	}
	f.live["c1"] = entity.LiveStream{StreamID: "s1", ChannelID: "c1", CategoryID: &cat}
	f.follows["v1/c1"] = entity.Follow{UserID: "v1", ChannelID: "c1", NotifyLevel: entity.NotifyLiveOnly}
	uc := newUC(f, fakeViewers{"s1": 42})

	tests := []struct {
		name, handle, viewer string
		wantErr              error
		check                func(t *testing.T, p entity.Profile)
	}{
		{name: "guest", handle: "minhnn", check: func(t *testing.T, p entity.Profile) {
			t.Helper()
			require.Nil(t, p.Viewer)
			require.Equal(t, 42, *p.CurrentStream.ViewerCount)
			require.Equal(t, "slug-cat1", p.CurrentStream.Category.Slug)
		}},
		{name: "follower", handle: "minhnn", viewer: "v1", check: func(t *testing.T, p entity.Profile) {
			t.Helper()
			require.True(t, p.Viewer.Following)
			require.Equal(t, entity.NotifyLiveOnly, *p.Viewer.NotifyLevel)
			require.False(t, p.Viewer.IsOwner)
		}},
		{name: "owner", handle: "minhnn", viewer: "u1", check: func(t *testing.T, p entity.Profile) {
			t.Helper()
			require.True(t, p.Viewer.IsOwner)
			require.False(t, p.Viewer.Following)
			require.Nil(t, p.Viewer.NotifyLevel)
		}},
		{name: "unknown", handle: "nobody", wantErr: entity.ErrNotFound},
		{name: "malformed", handle: "a-b", wantErr: entity.ErrNotFound},
		{name: "suspended", handle: "banned", wantErr: entity.ErrSuspended},
	}

	t.Run("suspended details", func(t *testing.T) {
		t.Parallel()

		_, err := uc.Profile(context.Background(), "banned", "")
		require.Equal(t, map[string]string{"handle": "banned", "displayName": "Banned", "avatarUrl": avatar}, entity.DetailsOf(err))
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := uc.Profile(context.Background(), tc.handle, tc.viewer)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			tc.check(t, p)
		})
	}
}

func TestFollow(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.byOwner["u1"] = entity.Channel{ID: "c1", OwnerID: "u1", Handle: "minhnn", Status: entity.StatusActive}
	f.byOwner["u2"] = entity.Channel{ID: "c2", OwnerID: "u2", Handle: "banned", Status: entity.StatusSuspended}
	uc := newUC(f, nil)
	ctx := context.Background()

	tests := []struct {
		name, user, channel string
		wantErr             error
	}{
		{name: "follows", user: "v1", channel: "c1"},
		{name: "again is idempotent", user: "v1", channel: "c1"},
		{name: "own channel", user: "u1", channel: "c1", wantErr: entity.ErrCannotFollowSelf},
		{name: "suspended", user: "v1", channel: "c2", wantErr: entity.ErrSuspended},
		{name: "unknown", user: "v1", channel: "c9", wantErr: entity.ErrNotFound},
	}

	// Sequential on purpose: the steps share the fake.
	for _, tc := range tests {
		fl, err := uc.Follow(ctx, tc.user, tc.channel)
		if tc.wantErr != nil {
			require.ErrorIs(t, err, tc.wantErr, tc.name)

			continue
		}

		require.NoError(t, err, tc.name)
		require.Equal(t, entity.NotifyAll, fl.NotifyLevel, tc.name)
	}

	require.Len(t, f.follows, 1)

	_, err := uc.SetNotifyLevel(ctx, "v2", "c1", entity.NotifyOff)
	require.ErrorIs(t, err, entity.ErrNotFollowing)

	_, err = uc.SetNotifyLevel(ctx, "v1", "c9", entity.NotifyOff)
	require.ErrorIs(t, err, entity.ErrNotFound)

	fl, err := uc.SetNotifyLevel(ctx, "v1", "c1", entity.NotifyOff)
	require.NoError(t, err)
	require.Equal(t, entity.NotifyOff, fl.NotifyLevel)

	require.NoError(t, uc.Unfollow(ctx, "v1", "c1"))
	require.NoError(t, uc.Unfollow(ctx, "v1", "c1"), "unfollowing twice is fine")
	require.Empty(t, f.follows)
}

func TestSidebarAndFollowing(t *testing.T) {
	t.Parallel()

	f := newFake()
	now := time.Now()

	for i, id := range []string{"a", "b", "c", "d"} {
		live := i < 3
		f.byOwner["o"+id] = entity.Channel{ID: id, OwnerID: "o" + id, Handle: "h" + id, Status: entity.StatusActive, IsLive: live}
		f.follows["v/"+id] = entity.Follow{UserID: "v", ChannelID: id, NotifyLevel: entity.NotifyAll}

		if live {
			f.live[id] = entity.LiveStream{StreamID: "s" + id, ChannelID: id, StartedAt: now.Add(time.Duration(i) * time.Minute)}
		}
	}

	// b has the most viewers; a and c tie, and c started later.
	uc := newUC(f, fakeViewers{"sa": 5, "sb": 50, "sc": 5})
	ctx := context.Background()

	sb, err := uc.Sidebar(ctx, "v", 2)
	require.NoError(t, err)
	require.Len(t, sb.Live, 2)
	require.Equal(t, "b", sb.Live[0].Channel.ID)
	require.Equal(t, "c", sb.Live[1].Channel.ID)
	require.Empty(t, sb.Offline, "live channels fill the limit first")
	require.Equal(t, 4, sb.TotalFollowing)

	sb, err = uc.Sidebar(ctx, "v", 10)
	require.NoError(t, err)
	require.Len(t, sb.Live, 3)
	require.Len(t, sb.Offline, 1)

	page, err := uc.Following(ctx, "v", entity.FollowingLive, "", 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.Equal(t, 50, *page.Items[0].CurrentStream.ViewerCount)

	_, err = uc.Following(ctx, "v", entity.FollowingAll, "%%%", 0)
	require.ErrorIs(t, err, entity.ErrValidation)
	require.Equal(t, map[string]string{"cursor": entity.DetailInvalid}, entity.DetailsOf(err))
}

func TestLiveState(t *testing.T) {
	t.Parallel()

	f := newFake()
	uc := newUC(f, nil)
	ctx := context.Background()

	require.NoError(t, uc.StreamStarted(ctx, "e1", entity.LiveStream{StreamID: "s1", ChannelID: "c1"}))
	require.Equal(t, "s1", f.live["c1"].StreamID)
	require.NoError(t, uc.StreamUpdated(ctx, "e2", entity.LiveStream{StreamID: "s1", ChannelID: "c1", Title: "new"}))
	require.Equal(t, "new", f.live["c1"].Title)
	require.NoError(t, uc.StreamEnded(ctx, "e3", "c1", "s1"))
	require.Empty(t, f.live)

	f.err = errDB
	require.ErrorIs(t, uc.StreamStarted(ctx, "e4", entity.LiveStream{StreamID: "s2", ChannelID: "c1"}), errDB)
	require.ErrorIs(t, uc.StreamEnded(ctx, "e5", "c1", "s2"), errDB)
}
