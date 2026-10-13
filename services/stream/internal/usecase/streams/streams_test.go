package streams_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/streams"
	"github.com/stretchr/testify/require"
)

type fakeKeys struct{ hashes map[string]string }

func (f *fakeKeys) ChannelByKeyHash(_ context.Context, h string) (string, error) {
	if ch, ok := f.hashes[h]; ok {
		return ch, nil
	}

	return "", entity.ErrInvalidStreamKey
}

func (f *fakeKeys) Replace(_ context.Context, ch, h string) (time.Time, error) {
	for k, v := range f.hashes {
		if v == ch {
			delete(f.hashes, k)
		}
	}

	f.hashes[h] = ch

	return time.Now(), nil
}

func (f *fakeKeys) Info(context.Context, string) (entity.StreamKeyInfo, error) {
	return entity.StreamKeyInfo{}, nil
}

type fakeSettings struct{ saved entity.Settings }

func (f *fakeSettings) GetSettings(context.Context, string) (entity.Settings, error) {
	return f.saved, nil
}

func (f *fakeSettings) SaveSettings(_ context.Context, s entity.Settings) error {
	f.saved = s

	return nil
}

type fakeStreams struct {
	repo.StreamRepo
	authorized []string
	page       []entity.Stream
	gotCursor  *repo.Cursor
	// creatorEndedAt is when the channel's latest stream was ended by its creator (zero = never).
	creatorEndedAt time.Time
	expiredVODs    int64
}

func (f *fakeStreams) EndedByCreatorSince(_ context.Context, _ string, since time.Time) (bool, error) {
	return !f.creatorEndedAt.IsZero() && f.creatorEndedAt.After(since), nil
}

func (f *fakeStreams) ExpireVODs(context.Context) (int64, error) { return f.expiredVODs, nil }

type recObserver struct{ results []string }

func (o *recObserver) Authorize(r string)     { o.results = append(o.results, r) }
func (*recObserver) Transition(entity.Status) {}
func (*recObserver) OnAir(int)                {}

func (f *fakeStreams) Authorize(_ context.Context, ch string, _ time.Duration) (entity.Grant, error) {
	f.authorized = append(f.authorized, ch)

	return entity.Grant{StreamID: "s1", ChannelID: ch}, nil
}

func (f *fakeStreams) ListOnAir(_ context.Context, after *repo.Cursor, limit int) ([]entity.Stream, error) {
	f.gotCursor = after
	if limit < len(f.page) {
		return f.page[:limit], nil
	}

	return f.page, nil
}

// fakeChannels mimics creator_channels; lookups counts ChannelsByID calls (one per page, no N+1).
type fakeChannels struct {
	byOwner map[string]entity.Channel
	err     error
	lookups int
	asked   []string
}

func newFakeChannels() *fakeChannels {
	return &fakeChannels{byOwner: map[string]entity.Channel{}}
}

func (f *fakeChannels) ChannelOfOwner(_ context.Context, ownerID string) (entity.Channel, error) {
	if f.err != nil {
		return entity.Channel{}, f.err
	}

	if c, ok := f.byOwner[ownerID]; ok {
		return c, nil
	}

	return entity.Channel{}, entity.ErrNotFound
}

func (f *fakeChannels) ChannelsByID(_ context.Context, ids []string) (map[string]entity.Channel, error) {
	f.lookups++
	f.asked = append(f.asked, ids...)

	if f.err != nil {
		return nil, f.err
	}

	out := map[string]entity.Channel{}

	for _, c := range f.byOwner {
		for _, id := range ids {
			if c.ID == id {
				out[id] = c
			}
		}
	}

	return out, nil
}

func (f *fakeChannels) UpsertChannel(_ context.Context, _ string, c entity.Channel) error {
	if f.err != nil {
		return f.err
	}

	f.byOwner[c.OwnerID] = c

	return nil
}

func newUC() (*streams.UseCase, *fakeKeys, *fakeSettings, *fakeStreams) {
	k, s, st := &fakeKeys{hashes: map[string]string{}}, &fakeSettings{}, &fakeStreams{}

	return streams.New(k, s, st, newFakeChannels(), time.Minute, time.Hour, logger.New("error")), k, s, st
}

func newUCWithChannels() (*streams.UseCase, *fakeStreams, *fakeChannels) {
	st, ch := &fakeStreams{}, newFakeChannels()
	uc := streams.New(&fakeKeys{hashes: map[string]string{}}, &fakeSettings{}, st, ch, time.Minute, time.Hour, logger.New("error"))

	return uc, st, ch
}

func TestKeyRoundTrip(t *testing.T) {
	t.Parallel()

	uc, keys, _, st := newUC()
	ctx := context.Background()

	key, err := uc.CreateKey(ctx, "ch1")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(key, "live_"))
	require.NotContains(t, keys.hashes, key, "only the hash is stored")

	g, err := uc.Authorize(ctx, key, "1.1.1.1")
	require.NoError(t, err)
	require.Equal(t, "ch1", g.ChannelID)

	newKey, err := uc.CreateKey(ctx, "ch1")
	require.NoError(t, err)

	_, err = uc.Authorize(ctx, key, "")
	require.ErrorIs(t, err, entity.ErrInvalidStreamKey, "old key revoked")

	_, err = uc.Authorize(ctx, "not-a-key", "")
	require.ErrorIs(t, err, entity.ErrInvalidStreamKey)

	_, err = uc.Authorize(ctx, newKey, "")
	require.NoError(t, err)
	require.Equal(t, []string{"ch1", "ch1"}, st.authorized)
}

func TestSettingsNormalised(t *testing.T) {
	t.Parallel()

	uc, _, set, _ := newUC()

	_, err := uc.UpdateSettings(context.Background(), entity.Settings{
		ChannelID: "ch1", Title: "  Rank  ", Tags: []string{" LoL ", "lol", "", "valorant"},
	})
	require.NoError(t, err)
	require.Equal(t, "Rank", set.saved.Title)
	require.Equal(t, []string{"LoL", "valorant"}, set.saved.Tags)
	require.Equal(t, "vi", set.saved.Language)
}

func TestListLiveCursor(t *testing.T) {
	t.Parallel()

	uc, _, _, st := newUC()
	now := time.Now()

	for i := range 3 {
		ts := now.Add(-time.Duration(i) * time.Minute)
		st.page = append(st.page, entity.Stream{ID: "0199d4a2-0000-7000-8000-00000000000" + string(rune('1'+i)), StartedAt: &ts})
	}

	p, err := uc.ListLive(context.Background(), "", 2)
	require.NoError(t, err)
	require.Len(t, p.Items, 2)
	require.NotEmpty(t, p.NextCursor)

	_, err = uc.ListLive(context.Background(), p.NextCursor, 2)
	require.NoError(t, err)
	require.Equal(t, p.Items[1].ID, st.gotCursor.ID)
	require.WithinDuration(t, *p.Items[1].StartedAt, st.gotCursor.StartedAt, time.Microsecond)

	_, err = uc.ListLive(context.Background(), "garbage!", 2)
	require.ErrorIs(t, err, streams.ErrBadCursor)
}

func TestAuthorizeCooldownAfterCreatorEnd(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		endedAgo  time.Duration // 0 = the channel never ended a stream by creator
		cooldown  time.Duration
		wantErr   error
		wantGrant bool
	}{
		{name: "inside cooldown", endedAgo: 10 * time.Second, cooldown: 30 * time.Second, wantErr: entity.ErrRecentlyEnded},
		{name: "after cooldown", endedAgo: 31 * time.Second, cooldown: 30 * time.Second, wantGrant: true},
		{name: "never ended by creator", cooldown: 30 * time.Second, wantGrant: true},
		{name: "cooldown disabled", endedAgo: time.Second, wantGrant: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			k, set, st := &fakeKeys{hashes: map[string]string{}}, &fakeSettings{}, &fakeStreams{}
			if tc.endedAgo > 0 {
				st.creatorEndedAt = now.Add(-tc.endedAgo)
			}

			obs := &recObserver{}
			uc := streams.New(k, set, st, newFakeChannels(), time.Minute, time.Hour, logger.New("error"),
				streams.EndCooldown(tc.cooldown), streams.WithObserver(obs), streams.WithClock(func() time.Time { return now }))

			key, err := uc.CreateKey(context.Background(), "ch1")
			require.NoError(t, err)

			_, err = uc.Authorize(context.Background(), key, "1.1.1.1")
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Empty(t, st.authorized, "no stream is created during the cooldown")
				require.Equal(t, []string{streams.AuthorizeRecentlyEnded}, obs.results)

				return
			}

			require.NoError(t, err)
			require.Equal(t, []string{"ch1"}, st.authorized)
			require.Equal(t, []string{streams.AuthorizeOK}, obs.results)
		})
	}
}

func TestExpireVODs(t *testing.T) {
	t.Parallel()

	uc, _, _, st := newUC()
	st.expiredVODs = 3

	n, err := uc.ExpireVODs(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
}
