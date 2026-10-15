package persistent_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestSetStatus(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ch := newChannel(t, r, "suspendme")
	action := uuid.NewString()
	ev := uuid.NewString()

	changed, err := r.SetStatus(ctx, ev, entity.StatusChange{ChannelID: ch.ID, Suspend: true, Reason: "repeated spam", ActionID: action})
	require.NoError(t, err)
	require.True(t, changed)

	changed, err = r.SetStatus(ctx, ev, entity.StatusChange{ChannelID: ch.ID, Suspend: true, Reason: "repeated spam", ActionID: action})
	require.NoError(t, err)
	require.False(t, changed, "redelivery")

	changed, err = r.SetStatus(ctx, uuid.NewString(), entity.StatusChange{ChannelID: ch.ID, Suspend: true, Reason: "again", ActionID: action})
	require.NoError(t, err)
	require.False(t, changed, "already suspended")

	got, err := r.ByID(ctx, ch.ID)
	require.NoError(t, err)
	require.True(t, got.Suspended())

	var docs int

	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT count(*) FROM search_documents WHERE entity_id = $1`, ch.ID).Scan(&docs))
	require.Zero(t, docs, "hidden from search")

	sus := lastPayload[events.ChannelSuspended](t, pg, events.TypeChannelSuspended)
	require.Equal(t, ch.OwnerID, sus.OwnerID)
	require.NotNil(t, sus.ActionID)
	require.Equal(t, action, *sus.ActionID)

	changed, err = r.SetStatus(ctx, uuid.NewString(), entity.StatusChange{ChannelID: ch.ID, Reason: "appeal accepted", ActionID: uuid.NewString()})
	require.NoError(t, err)
	require.True(t, changed)

	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT count(*) FROM search_documents WHERE entity_id = $1`, ch.ID).Scan(&docs))
	require.Equal(t, 1, docs, "channel document back")
	require.Contains(t, outboxTypes(t, pg), events.TypeChannelRestored)
}

func TestSuspendedSchedulesAreQuiet(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	ch := newChannel(t, r, "quietch")
	now := time.Now().UTC().Truncate(time.Second)

	_, err := r.Create(ctx, weekly(t, ch, now.Add(2*time.Hour), ""), 5, now)
	require.NoError(t, err)

	_, err = r.SetStatus(ctx, uuid.NewString(), entity.StatusChange{ChannelID: ch.ID, Suspend: true, Reason: "repeated spam", ActionID: uuid.NewString()})
	require.NoError(t, err)

	n, err := r.AnnounceDue(ctx, now.Add(24*time.Hour), 10, func(entity.Schedule) []events.ScheduleOccurrenceUpcoming {
		t.Fatal("a suspended channel announces nothing")

		return nil
	})
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestSnapshots(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	a, b := newChannel(t, r, "snapa"), newChannel(t, r, "snapb")
	user := uuid.NewString()

	_, _, err := r.Follow(ctx, user, a.ID)
	require.NoError(t, err)

	_, _, err = r.Follow(ctx, user, b.ID)
	require.NoError(t, err)

	first, err := r.Channels(ctx, "", 1)
	require.NoError(t, err)
	require.Len(t, first, 1)

	rest, err := r.Channels(ctx, first[0].ID, 10)
	require.NoError(t, err)
	require.Len(t, rest, 1)

	follows, err := r.Follows(ctx, nil, 1)
	require.NoError(t, err)
	require.Len(t, follows, 1)

	more, err := r.Follows(ctx, &follows[0], 10)
	require.NoError(t, err)
	require.Len(t, more, 1)
	require.Equal(t, "ALL", more[0].NotifyLevel)
}
