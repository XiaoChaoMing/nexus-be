package streams_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/stretchr/testify/require"
)

var errChannelsDown = errors.New("db down")

func (f *fakeStreams) Get(_ context.Context, id string) (entity.Stream, error) {
	for _, s := range f.page {
		if s.ID == id {
			return s, nil
		}
	}

	return entity.Stream{}, entity.ErrNotFound
}

func (*fakeStreams) GetVOD(_ context.Context, id string) (entity.VOD, error) {
	if id != "v1" {
		return entity.VOD{}, entity.ErrNotFound
	}

	return entity.VOD{ID: "v1", ChannelID: "ch1"}, nil
}

func TestStudioChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		owner   string
		err     error
		wantID  string
		wantErr error
	}{
		{name: "creator", owner: "u1", wantID: "ch1"},
		{name: "not a creator", owner: "u2", wantErr: entity.ErrNotCreator},
		{name: "storage down", owner: "u1", err: errChannelsDown, wantErr: errChannelsDown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc, _, ch := newUCWithChannels()
			ch.byOwner["u1"] = entity.Channel{ID: "ch1", OwnerID: "u1", Handle: "minhnn"}
			ch.err = tc.err

			got, err := uc.Channel(context.Background(), tc.owner)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.wantID, got.ID)
		})
	}
}

func TestChannelCreated(t *testing.T) {
	t.Parallel()

	uc, _, ch := newUCWithChannels()
	c := entity.Channel{ID: "ch1", OwnerID: "u1", Handle: "minhnn", DisplayName: "Minh"}

	require.NoError(t, uc.ChannelCreated(context.Background(), "e1", c))
	require.Equal(t, c, ch.byOwner["u1"])

	got, err := uc.Channel(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, "ch1", got.ID)

	ch.err = errChannelsDown
	require.ErrorIs(t, uc.ChannelCreated(context.Background(), "e2", c), errChannelsDown)
}

func TestCatalogAttachesChannels(t *testing.T) {
	t.Parallel()

	uc, st, ch := newUCWithChannels()
	ch.byOwner["u1"] = entity.Channel{ID: "ch1", OwnerID: "u1", Handle: "minhnn", DisplayName: "Minh"}

	now := time.Now()
	st.page = []entity.Stream{
		{ID: "0199d4a2-0000-7000-8000-000000000001", ChannelID: "ch1", StartedAt: &now},
		{ID: "0199d4a2-0000-7000-8000-000000000002", ChannelID: "legacy", StartedAt: &now},
		{ID: "0199d4a2-0000-7000-8000-000000000003", ChannelID: "ch1", StartedAt: &now},
	}

	p, err := uc.ListLive(context.Background(), "", 10)
	require.NoError(t, err)
	require.Len(t, p.Items, 3)
	require.Equal(t, 1, ch.lookups, "one batched lookup per page")
	require.NotNil(t, p.Items[0].Channel)
	require.Equal(t, "minhnn", p.Items[0].Channel.Handle)
	require.Nil(t, p.Items[1].Channel, "unknown channel stays null")
	require.NotNil(t, p.Items[2].Channel)

	s, err := uc.Get(context.Background(), st.page[0].ID)
	require.NoError(t, err)
	require.NotNil(t, s.Channel)
	require.Equal(t, "Minh", s.Channel.DisplayName)

	_, err = uc.Get(context.Background(), "missing")
	require.ErrorIs(t, err, entity.ErrNotFound)

	v, err := uc.GetVOD(context.Background(), "v1")
	require.NoError(t, err)
	require.NotNil(t, v.Channel)
	require.Equal(t, "ch1", v.Channel.ID)

	_, err = uc.GetVOD(context.Background(), "nope")
	require.ErrorIs(t, err, entity.ErrNotFound)
}

func TestCatalogChannelLookupError(t *testing.T) {
	t.Parallel()

	uc, st, ch := newUCWithChannels()
	now := time.Now()
	st.page = []entity.Stream{{ID: "0199d4a2-0000-7000-8000-000000000001", ChannelID: "ch1", StartedAt: &now}}
	ch.err = errChannelsDown

	_, err := uc.ListLive(context.Background(), "", 10)
	require.ErrorIs(t, err, errChannelsDown)

	_, err = uc.Get(context.Background(), st.page[0].ID)
	require.ErrorIs(t, err, errChannelsDown)

	_, err = uc.GetVOD(context.Background(), "v1")
	require.ErrorIs(t, err, errChannelsDown)
}

func TestListLiveEmptySkipsLookup(t *testing.T) {
	t.Parallel()

	uc, _, ch := newUCWithChannels()

	p, err := uc.ListLive(context.Background(), "", 10)
	require.NoError(t, err)
	require.Empty(t, p.Items)
	require.Zero(t, ch.lookups)
}
