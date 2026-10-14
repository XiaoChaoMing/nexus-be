package streams_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/streams"
	"github.com/stretchr/testify/require"
)

var errRedisDown = errors.New("redis down")

type fakeViewerCounts struct {
	counts map[string]int
	err    error
	asked  []string
}

func (f *fakeViewerCounts) ViewerCounts(_ context.Context, ids []string) (map[string]int, error) {
	f.asked = append(f.asked, ids...)

	return f.counts, f.err
}

func TestCatalogViewerCounts(t *testing.T) {
	t.Parallel()

	started := time.Now()
	page := []entity.Stream{
		{ID: "s1", ChannelID: "ch1", Status: entity.StatusLive, StartedAt: &started},
		{ID: "s2", ChannelID: "ch1", Status: entity.StatusInterrupted, StartedAt: &started},
	}

	tests := []struct {
		name string
		vc   *fakeViewerCounts
		want map[string]*int
	}{
		{name: "counts known for one stream", vc: &fakeViewerCounts{counts: map[string]int{"s1": 3}}, want: map[string]*int{"s1": new(3), "s2": nil}},
		{name: "redis down: null counts, no error", vc: &fakeViewerCounts{err: errRedisDown}, want: map[string]*int{"s1": nil, "s2": nil}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := &fakeStreams{page: append([]entity.Stream(nil), page...)}
			uc := streams.New(&fakeKeys{hashes: map[string]string{}}, &fakeSettings{}, st, newFakeChannels(), time.Minute, time.Hour,
				logger.New("error"), streams.WithViewerCounts(tc.vc))

			got, err := uc.ListLive(context.Background(), "", 10)
			require.NoError(t, err)

			for _, s := range got.Items {
				require.Equal(t, tc.want[s.ID], s.ViewerCount, s.ID)
			}

			one, err := uc.Get(context.Background(), "s1")
			require.NoError(t, err)
			require.Equal(t, tc.want["s1"], one.ViewerCount)
			require.Contains(t, tc.vc.asked, "s2")
		})
	}
}

func TestCatalogWithoutViewerCounts(t *testing.T) {
	t.Parallel()

	uc, _, _, st := newUC()
	st.page = []entity.Stream{{ID: "s1", ChannelID: "ch1", Status: entity.StatusLive}}

	got, err := uc.Get(context.Background(), "s1")
	require.NoError(t, err)
	require.Nil(t, got.ViewerCount)
}
