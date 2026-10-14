package events_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	pkgevents "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/stretchr/testify/require"
)

type recChannels struct{ got []entity.Channel }

func (r *recChannels) ChannelCreated(_ context.Context, _ string, c entity.Channel) error {
	r.got = append(r.got, c)

	return nil
}

// The ingest use case is never reached by channel events.
type noIngest struct{ usecase.Ingest }

func channelPayload(channelID, ownerID, handle, displayName string) []byte {
	return []byte(`{"channelId":"` + channelID + `","ownerId":"` + ownerID + `","handle":"` + handle +
		`","displayName":"` + displayName + `"}`)
}

func TestChannelCreated(t *testing.T) {
	t.Parallel()

	rec := &recChannels{}
	ch, owner := uuid.NewString(), uuid.NewString()

	err := events.Handler(noIngest{}, rec)(context.Background(), eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeChannelCreated, Payload: channelPayload(ch, owner, "minhnn", "Minh"),
	})
	require.NoError(t, err)
	require.Equal(t, []entity.Channel{{ID: ch, OwnerID: owner, Handle: "minhnn", DisplayName: "Minh"}}, rec.got)
	require.Contains(t, events.Types, pkgevents.TypeChannelCreated)
}

func TestChannelCreatedSkipsBadPayloads(t *testing.T) {
	t.Parallel()

	ch, owner := uuid.NewString(), uuid.NewString()

	tests := []struct {
		name    string
		id      string
		payload []byte
	}{
		{name: "not json", payload: []byte(`{`)},
		{name: "bad channel id", payload: channelPayload("x", owner, "minhnn", "Minh")},
		{name: "bad owner id", payload: channelPayload(ch, "x", "minhnn", "Minh")},
		{name: "empty handle", payload: channelPayload(ch, owner, "", "Minh")},
		{name: "long handle", payload: channelPayload(ch, owner, strings.Repeat("a", 26), "Minh")},
		{name: "empty display name", payload: channelPayload(ch, owner, "minhnn", "")},
		{name: "long display name", payload: channelPayload(ch, owner, "minhnn", strings.Repeat("ă", 51))},
		{name: "event id not uuid", id: "nope", payload: channelPayload(ch, owner, "minhnn", "Minh")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recChannels{}
			err := events.Handler(noIngest{}, rec)(context.Background(), eventbus.Event{
				ID: tc.id, Type: pkgevents.TypeChannelCreated, Payload: tc.payload,
			})
			require.ErrorIs(t, err, eventbus.ErrSkip)
			require.Empty(t, rec.got)
		})
	}
}

type recPeak struct {
	usecase.Ingest
	streamID string
	peak     int
}

func (r *recPeak) PeakViewers(_ context.Context, _, streamID string, peak int) error {
	r.streamID, r.peak = streamID, peak

	return nil
}

func TestPeakViewers(t *testing.T) {
	t.Parallel()

	id := uuid.NewString()
	rec := &recPeak{}
	h := events.Handler(rec, &recChannels{})

	err := h(context.Background(), eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeStreamPeakViewers, Payload: []byte(`{"streamId":"` + id + `","peakViewers":7}`),
	})
	require.NoError(t, err)
	require.Equal(t, id, rec.streamID)
	require.Equal(t, 7, rec.peak)
	require.Contains(t, events.Types, pkgevents.TypeStreamPeakViewers)

	err = h(context.Background(), eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeStreamPeakViewers, Payload: []byte(`{"streamId":"` + id + `","peakViewers":-1}`),
	})
	require.ErrorIs(t, err, eventbus.ErrSkip)
}
