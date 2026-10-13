package events_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	pkgevents "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/stretchr/testify/require"
)

type recSync struct {
	approved []usecase.CreatorApproved
	started  []string
	ended    []string
}

func (r *recSync) CreatorApproved(_ context.Context, _ string, in usecase.CreatorApproved) error {
	r.approved = append(r.approved, in)

	return nil
}

func (r *recSync) StreamStarted(_ context.Context, _, channelID, streamID string) error {
	r.started = append(r.started, channelID+"/"+streamID)

	return nil
}

func (r *recSync) StreamEnded(_ context.Context, _, channelID, streamID string) error {
	r.ended = append(r.ended, channelID+"/"+streamID)

	return nil
}

func TestHandlerDecodes(t *testing.T) {
	t.Parallel()

	rec := &recSync{}
	h := events.Handler(rec)
	ctx := context.Background()
	user, ch, st := uuid.NewString(), uuid.NewString(), uuid.NewString()

	require.NoError(t, h(ctx, eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeCreatorApproved,
		Payload: []byte(`{"applicationId":"a","userId":"` + user + `","username":"minhnn","displayName":"Minh"}`),
	}))
	require.NoError(t, h(ctx, eventbus.Event{
		ID: uuid.NewString(), Type: pkgevents.TypeStreamStarted,
		Payload: []byte(`{"streamId":"` + st + `","channelId":"` + ch + `","title":"x"}`),
	}))
	require.NoError(t, h(ctx, eventbus.Event{
		Type:    pkgevents.TypeStreamEnded,
		Payload: []byte(`{"streamId":"` + st + `","channelId":"` + ch + `","endReason":"creator"}`),
	}))

	require.Equal(t, []usecase.CreatorApproved{{UserID: user, Username: "minhnn", DisplayName: "Minh"}}, rec.approved)
	require.Equal(t, []string{ch + "/" + st}, rec.started)
	require.Equal(t, []string{ch + "/" + st}, rec.ended)
}

func TestHandlerSkipsBadPayloads(t *testing.T) {
	t.Parallel()

	user, id := uuid.NewString(), uuid.NewString()

	tests := []struct {
		name string
		e    eventbus.Event
	}{
		{name: "not json", e: eventbus.Event{ID: id, Type: pkgevents.TypeCreatorApproved, Payload: []byte(`{`)}},
		{name: "bad user id", e: eventbus.Event{ID: id, Type: pkgevents.TypeCreatorApproved, Payload: []byte(`{"userId":"x","username":"minhnn"}`)}},
		{name: "empty username", e: eventbus.Event{ID: id, Type: pkgevents.TypeCreatorApproved, Payload: []byte(`{"userId":"` + user + `","username":" "}`)}},
		{name: "event id not uuid", e: eventbus.Event{ID: "nope", Type: pkgevents.TypeCreatorApproved, Payload: []byte(`{"userId":"` + user + `","username":"minhnn"}`)}},
		{name: "bad channel id", e: eventbus.Event{ID: id, Type: pkgevents.TypeStreamStarted, Payload: []byte(`{"streamId":"` + user + `","channelId":"x"}`)}},
		{name: "bad stream id", e: eventbus.Event{ID: id, Type: pkgevents.TypeStreamEnded, Payload: []byte(`{"streamId":"x","channelId":"` + user + `"}`)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recSync{}
			err := events.Handler(rec)(context.Background(), tc.e)
			require.ErrorIs(t, err, eventbus.ErrSkip)
			require.Empty(t, rec.approved)
			require.Empty(t, rec.started)
			require.Empty(t, rec.ended)
		})
	}
}
