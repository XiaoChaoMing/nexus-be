package streams_test

import (
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/streams"
	"github.com/stretchr/testify/require"
)

// catalogStreams serves a fixed set of streams on air and one stream / VOD for the detail lookups.
type catalogStreams struct {
	repo.StreamRepo
	onAir  []entity.Stream
	stream entity.Stream
	vod    entity.VOD
}

func (f *catalogStreams) ListOnAirAll(context.Context, repo.StreamFilter, int) ([]entity.Stream, error) {
	return append([]entity.Stream(nil), f.onAir...), nil
}

func (f *catalogStreams) Get(context.Context, string) (entity.Stream, error) { return f.stream, nil }

func (f *catalogStreams) GetVOD(context.Context, string) (entity.VOD, error) { return f.vod, nil }

type viewerCounts map[string]int

func (v viewerCounts) ViewerCounts(_ context.Context, ids []string) (map[string]int, error) {
	out := map[string]int{}

	for _, id := range ids {
		if n, ok := v[id]; ok {
			out[id] = n
		}
	}

	return out, nil
}

func onAir(id string, status entity.Status, mature bool, started time.Time) entity.Stream {
	return entity.Stream{ID: id, ChannelID: "ch-" + id, Status: status, IsMature: mature, StartedAt: &started}
}

func catalogUC(st *catalogStreams, ch *fakeChannels, counts viewerCounts) *streams.UseCase {
	return streams.New(&fakeKeys{hashes: map[string]string{}}, &fakeSettings{}, st, ch, time.Minute, time.Hour, logger.New("error"),
		streams.WithViewerCounts(counts))
}

func TestListByViewers(t *testing.T) {
	t.Parallel()

	now := time.Now()
	st := &catalogStreams{onAir: []entity.Stream{
		onAir("00000000-0000-7000-8000-00000000000a", entity.StatusLive, false, now),
		onAir("00000000-0000-7000-8000-00000000000b", entity.StatusLive, false, now.Add(-time.Hour)),
		onAir("00000000-0000-7000-8000-00000000000c", entity.StatusInterrupted, true, now.Add(-2*time.Hour)),
		onAir("00000000-0000-7000-8000-00000000000d", entity.StatusLive, false, now.Add(-3*time.Hour)),
	}}
	// b leads; a and d tie and a started later.
	uc := catalogUC(st, newFakeChannels(), viewerCounts{
		"00000000-0000-7000-8000-00000000000a": 5, "00000000-0000-7000-8000-00000000000b": 50,
		"00000000-0000-7000-8000-00000000000c": 7, "00000000-0000-7000-8000-00000000000d": 5,
	})
	ctx := context.Background()

	var got []string

	cursor := ""

	for range 5 {
		page, err := uc.List(ctx, usecase.StreamFilter{Sort: usecase.SortViewers}, cursor, 3)
		require.NoError(t, err)

		for _, s := range page.Items {
			got = append(got, s.ID[len(s.ID)-1:])
		}

		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}

	require.Equal(t, []string{"b", "c", "a", "d"}, got, "viewers, then the latest start; every stream once")

	_, err := uc.List(ctx, usecase.StreamFilter{Sort: usecase.SortTrending}, "not-base64!", 3)
	require.ErrorIs(t, err, streams.ErrBadCursor)

	rec, err := uc.Recommended(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rec, 3, "no INTERRUPTED and no 18+ stream")
	require.Equal(t, "00000000-0000-7000-8000-00000000000b", rec[0].ID)
}

func TestHiddenVOD(t *testing.T) {
	t.Parallel()

	vodID, visibility := "v1", entity.VisibilityHidden
	st := &catalogStreams{
		stream: entity.Stream{ID: "s1", ChannelID: "c1", Status: entity.StatusEnded, VODID: &vodID, VODVisibility: &visibility},
		vod:    entity.VOD{ID: vodID, ChannelID: "c1", Visibility: entity.VisibilityHidden},
	}
	ch := newFakeChannels()
	ch.byOwner["owner"] = entity.Channel{ID: "c1", OwnerID: "owner"}
	ch.byOwner["other"] = entity.Channel{ID: "c2", OwnerID: "other"}
	uc := catalogUC(st, ch, nil)
	ctx := context.Background()

	for _, viewer := range []string{"", "other", "nobody"} {
		_, err := uc.GetVODFor(ctx, vodID, viewer)
		require.ErrorIs(t, err, entity.ErrNotFound, "viewer %q", viewer)

		s, err := uc.GetFor(ctx, "s1", viewer)
		require.NoError(t, err)
		require.Nil(t, s.VODID, "viewer %q", viewer)
	}

	v, err := uc.GetVODFor(ctx, vodID, "owner")
	require.NoError(t, err)
	require.Equal(t, vodID, v.ID)

	s, err := uc.GetFor(ctx, "s1", "owner")
	require.NoError(t, err)
	require.Equal(t, vodID, *s.VODID)
}

func TestDashboardRange(t *testing.T) {
	t.Parallel()

	_, err := catalogUC(&catalogStreams{}, newFakeChannels(), nil).Dashboard(context.Background(), "c1", "1y")
	require.ErrorIs(t, err, entity.ErrInvalidState)
}
