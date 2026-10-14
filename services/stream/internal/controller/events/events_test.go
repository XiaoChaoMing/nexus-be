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

func (r *recChannels) ChannelUpdated(_ context.Context, _ string, c entity.Channel) error {
	r.got = append(r.got, c)

	return nil
}

type recSchedules struct {
	upcoming []pkgevents.ScheduleOccurrenceUpcoming
	changed  []pkgevents.ScheduleOccurrenceChanged
	updated  []pkgevents.ScheduleUpdated
}

func (r *recSchedules) OccurrenceUpcoming(_ context.Context, _ string, e pkgevents.ScheduleOccurrenceUpcoming) error {
	r.upcoming = append(r.upcoming, e)

	return nil
}

func (r *recSchedules) OccurrenceChanged(_ context.Context, _ string, e pkgevents.ScheduleOccurrenceChanged) error {
	r.changed = append(r.changed, e)

	return nil
}

func (r *recSchedules) ScheduleUpdated(_ context.Context, _ string, e pkgevents.ScheduleUpdated) error {
	r.updated = append(r.updated, e)

	return nil
}

func (*recSchedules) CancelLate(context.Context) (int, error) { return 0, nil }

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

	err := events.Handler(noIngest{}, rec, &recSchedules{})(context.Background(), eventbus.Event{
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
			err := events.Handler(noIngest{}, rec, &recSchedules{})(context.Background(), eventbus.Event{
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
	h := events.Handler(rec, &recChannels{}, &recSchedules{})

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

func TestChannelUpdated(t *testing.T) {
	t.Parallel()

	rec := &recChannels{}
	ch, owner := uuid.NewString(), uuid.NewString()

	err := events.Handler(noIngest{}, rec, &recSchedules{})(context.Background(), eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeChannelUpdated,
		Payload: []byte(`{"channelId":"` + ch + `","ownerId":"` + owner + `","handle":"new_handle","displayName":"Minh","avatarUrl":"/media/a.jpg"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "new_handle", rec.got[0].Handle)
	require.Equal(t, "/media/a.jpg", *rec.got[0].AvatarURL)
}

func TestScheduleEvents(t *testing.T) {
	t.Parallel()

	rec := &recSchedules{}
	h := events.Handler(noIngest{}, &recChannels{}, rec)
	ctx := context.Background()
	sched, ch := uuid.NewString(), uuid.NewString()

	require.NoError(t, h(ctx, eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeScheduleOccurrenceUpcoming,
		Payload: []byte(`{"scheduleId":"` + sched + `","channelId":"` + ch + `","title":"Rank","occurrenceAt":"2026-10-20T13:00:00Z",` +
			`"categoryId":"bogus","durationMin":60}`),
	}))
	require.Len(t, rec.upcoming, 1)
	require.Nil(t, rec.upcoming[0].CategoryID, "a malformed category id is dropped")

	require.NoError(t, h(ctx, eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeScheduleOccurrenceChanged,
		Payload: []byte(`{"scheduleId":"` + sched + `","channelId":"` + ch + `","occurrenceAt":"2026-10-20T13:00:00Z","reason":"CANCELLED"}`),
	}))
	require.Len(t, rec.changed, 1)

	require.NoError(t, h(ctx, eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeScheduleUpdated,
		Payload: []byte(`{"scheduleId":"` + sched + `","channelId":"` + ch + `","title":"New"}`),
	}))
	require.Len(t, rec.updated, 1)

	for _, bad := range []string{
		`{"scheduleId":"x","channelId":"` + ch + `","occurrenceAt":"2026-10-20T13:00:00Z"}`,
		`{"scheduleId":"` + sched + `","channelId":"x","occurrenceAt":"2026-10-20T13:00:00Z"}`,
		`{"scheduleId":"` + sched + `","channelId":"` + ch + `"}`,
	} {
		err := h(ctx, eventbus.Event{ID: uuid.NewString(), Type: pkgevents.TypeScheduleOccurrenceUpcoming, Payload: []byte(bad)})
		require.ErrorIs(t, err, eventbus.ErrSkip, bad)
	}
}
