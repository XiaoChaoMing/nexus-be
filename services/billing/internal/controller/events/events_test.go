package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	billingevents "github.com/minhnn/nexuslive-be/services/billing/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/require"
)

const (
	eventID = "0199f0a3-0000-7000-8000-000000000001"
	chID    = "0199f0a3-0000-7000-8000-000000000002"
	ownerID = "0199f0a3-0000-7000-8000-000000000003"
)

type replica struct {
	usecase.Replica

	calls []string
}

func (r *replica) ChannelCreated(_ context.Context, id string, _ time.Time, e events.ChannelCreated) error {
	r.calls = append(r.calls, "created "+id+" "+e.Handle)

	return nil
}

func (r *replica) ChannelUpdated(_ context.Context, _ string, _ time.Time, e events.ChannelUpdated) error {
	r.calls = append(r.calls, "updated "+e.Handle)

	return nil
}

func (r *replica) ChannelStatus(_ context.Context, _ string, _ time.Time, _ string, suspended bool) error {
	r.calls = append(r.calls, map[bool]string{true: "suspended", false: "restored"}[suspended])

	return nil
}

func ev(t *testing.T, typ, id string, payload any) eventbus.Event {
	t.Helper()

	b, err := json.Marshal(payload)
	require.NoError(t, err)

	return eventbus.Event{ID: id, Type: typ, Payload: b, OccurredAt: time.Now()}
}

func TestHandler(t *testing.T) {
	t.Parallel()

	r := &replica{}
	h := billingevents.Handler(r)
	ctx := context.Background()
	ch := events.ChannelCreated{ChannelID: chID, OwnerID: ownerID, Handle: "kaelen"}

	require.NoError(t, h(ctx, ev(t, events.TypeChannelCreated, eventID, ch)))
	require.NoError(t, h(ctx, ev(t, events.TypeChannelUpdated, eventID, events.ChannelUpdated{
		ChannelID: chID, OwnerID: ownerID,
		Handle: "new",
	})))
	require.NoError(t, h(ctx, ev(t, events.TypeChannelSuspended, eventID, events.ChannelSuspended{ChannelID: chID})))
	require.NoError(t, h(ctx, ev(t, events.TypeChannelRestored, eventID, events.ChannelSuspended{ChannelID: chID})))
	require.Equal(t, []string{"created " + eventID + " kaelen", "updated new", "suspended", "restored"}, r.calls)

	// Payloads that can never succeed are skipped, not retried.
	for _, bad := range []eventbus.Event{
		ev(t, events.TypeChannelCreated, "not-a-uuid", ch),
		ev(t, events.TypeChannelCreated, eventID, events.ChannelCreated{ChannelID: "x", OwnerID: ownerID}),
		{ID: eventID, Type: events.TypeChannelCreated, Payload: []byte(`{`)},
		ev(t, events.TypeStreamStarted, eventID, ch),
	} {
		require.ErrorIs(t, h(ctx, bad), eventbus.ErrSkip, bad.Type)
	}

	require.Len(t, billingevents.Types(), 4)
}
