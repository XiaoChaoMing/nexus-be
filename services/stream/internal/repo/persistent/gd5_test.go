package persistent_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/stretchr/testify/require"
)

func TestTakedownLive(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()

	g, err := r.Authorize(ctx, ch, window())
	require.NoError(t, err)

	ev := uuid.NewString()
	res, err := r.Takedown(ctx, ev, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.TakedownResult{Found: true, Ended: true}, res)

	again, err := r.Takedown(ctx, ev, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.TakedownResult{}, again, "redelivery")

	s, err := r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusEnded, s.Status)
	require.Equal(t, entity.EndAdminTakedown, *s.EndReason)
	require.Equal(t, events.EndReasonAdminTakedown, lastPayload[events.StreamEnded](t, pg, events.TypeStreamEnded).EndReason)

	// The VOD media-svc finishes afterwards is recorded DELETED.
	require.NoError(t, r.OnVODProcessed(ctx, uuid.NewString(), repo.VODInput{
		StreamID: g.StreamID, DurationSec: 30, PlaylistPath: "streams/" + g.StreamID + "/master.m3u8", Retention: time.Hour,
	}))

	s, err = r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Nil(t, s.VODID, "no READY VOD")
	require.Equal(t, g.StreamID, lastPayload[events.VODDeleted](t, pg, events.TypeVODDeleted).StreamID)
	require.NotContains(t, outboxTypes(t, pg), events.TypeStreamVODReady)
}

func TestTakedownEndedWithVOD(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	v := vod(t, r, uuid.NewString(), false, nil)

	res, err := r.Takedown(ctx, uuid.NewString(), v.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.TakedownResult{Found: true, VODsDeleted: 1}, res)

	_, err = r.GetVOD(ctx, v.ID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	s, err := r.Get(ctx, v.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.EndCreator, *s.EndReason, "an ended stream keeps its reason")
	require.Equal(t, v.ID, lastPayload[events.VODDeleted](t, pg, events.TypeVODDeleted).VODID)

	// Unknown stream: nothing.
	res, err = r.Takedown(ctx, uuid.NewString(), uuid.NewString())
	require.NoError(t, err)
	require.False(t, res.Found)
}

func TestSuspendedChannel(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	owner, ch := uuid.NewString(), uuid.NewString()

	require.NoError(t, r.UpsertChannel(ctx, "", entity.Channel{ID: ch, OwnerID: owner, Handle: "minhnn", DisplayName: "Minh"}))

	g, err := r.Authorize(ctx, ch, window())
	require.NoError(t, err)
	require.NoError(t, r.CreateScheduled(ctx, uuid.NewString(), upcoming(ch, uuid.NewString(), time.Now().Add(48*time.Hour))))

	ended, cancelled, err := r.SetSuspended(ctx, uuid.NewString(), ch, true)
	require.NoError(t, err)
	require.Equal(t, 1, ended)
	require.Equal(t, 1, cancelled)

	s, err := r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.EndAdminTakedown, *s.EndReason)
	require.Equal(t, events.StreamStatusCancelled, lastPayload[events.StreamStatusChanged](t, pg, events.TypeStreamStatusChanged).Status)

	_, err = r.Authorize(ctx, ch, window())
	require.ErrorIs(t, err, entity.ErrChannelSuspended)

	got, err := r.ChannelOfOwner(ctx, owner)
	require.NoError(t, err)
	require.True(t, got.Suspended)

	_, _, err = r.SetSuspended(ctx, uuid.NewString(), ch, false)
	require.NoError(t, err)

	_, err = r.Authorize(ctx, ch, window())
	require.NoError(t, err, "restored")

	copies, err := r.Streams(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, copies, 3)
}
