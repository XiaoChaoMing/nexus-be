package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	pkgevents "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/stretchr/testify/require"
)

type recSync struct {
	approved  []usecase.CreatorApproved
	started   []entity.LiveStream
	updated   []entity.LiveStream
	ended     []string
	scheduled []pkgevents.StreamScheduled
	vods      []entity.VODDoc
	deleted   []string
}

func (r *recSync) CreatorApproved(_ context.Context, _ string, in usecase.CreatorApproved) error {
	r.approved = append(r.approved, in)

	return nil
}

func (r *recSync) StreamStarted(_ context.Context, _ string, s entity.LiveStream) error {
	r.started = append(r.started, s)

	return nil
}

func (r *recSync) StreamUpdated(_ context.Context, _ string, s entity.LiveStream) error {
	r.updated = append(r.updated, s)

	return nil
}

func (r *recSync) StreamEnded(_ context.Context, _, channelID, streamID string) error {
	r.ended = append(r.ended, channelID+"/"+streamID)

	return nil
}

func (r *recSync) StreamScheduled(_ context.Context, _ string, e pkgevents.StreamScheduled) error {
	r.scheduled = append(r.scheduled, e)

	return nil
}

func (r *recSync) VODChanged(_ context.Context, _ string, v entity.VODDoc) error {
	r.vods = append(r.vods, v)

	return nil
}

func (r *recSync) VODDeleted(_ context.Context, _, vodID string) error {
	r.deleted = append(r.deleted, vodID)

	return nil
}

func (r *recSync) empty() bool {
	return len(r.approved)+len(r.started)+len(r.updated)+len(r.ended)+len(r.scheduled)+len(r.vods)+len(r.deleted) == 0
}

func TestHandlerDecodes(t *testing.T) {
	t.Parallel()

	rec := &recSync{}
	h := events.Handler(rec, nil)
	ctx := context.Background()
	user, ch, st, cat, vod := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()

	send := func(typ, payload string) {
		t.Helper()
		require.NoError(t, h(ctx, eventbus.Event{ID: uuid.NewString(), Type: typ, Payload: []byte(payload)}))
	}

	send(pkgevents.TypeCreatorApproved, `{"applicationId":"a","userId":"`+user+`","username":"minhnn","displayName":"Minh"}`)
	send(pkgevents.TypeStreamStarted, `{"streamId":"`+st+`","channelId":"`+ch+`","title":"x","categoryId":"`+cat+
		`","tags":["fps"],"language":"en","isMature":true,"startedAt":"2026-10-15T10:00:00Z"}`)
	// An event from before GĐ4: no category, language or start time.
	send(pkgevents.TypeStreamStarted, `{"streamId":"`+st+`","channelId":"`+ch+`","title":"old"}`)
	send(pkgevents.TypeStreamUpdated, `{"streamId":"`+st+`","channelId":"`+ch+`","title":"y","categoryId":"not-a-uuid"}`)
	send(pkgevents.TypeStreamEnded, `{"streamId":"`+st+`","channelId":"`+ch+`","endReason":"creator"}`)
	send(pkgevents.TypeStreamScheduled, `{"streamId":"`+st+`","channelId":"`+ch+`","scheduleId":"`+cat+`","scheduledStartAt":"2026-10-15T13:00:00Z"}`)
	send(pkgevents.TypeStreamVODReady, `{"streamId":"`+st+`","channelId":"`+ch+`","vodId":"`+vod+`","title":"v","expiresAt":"2026-10-29T00:00:00Z"}`)
	send(pkgevents.TypeVODUpdated, `{"vodId":"`+vod+`","streamId":"`+st+`","channelId":"`+ch+`","title":"v","visibility":"HIDDEN","status":"READY"}`)
	send(pkgevents.TypeVODDeleted, `{"vodId":"`+vod+`","streamId":"`+st+`","channelId":"`+ch+`"}`)

	require.Equal(t, []usecase.CreatorApproved{{UserID: user, Username: "minhnn", DisplayName: "Minh"}}, rec.approved)
	require.Len(t, rec.started, 2)
	require.Equal(t, cat, *rec.started[0].CategoryID)
	require.Equal(t, []string{"fps"}, rec.started[0].Tags)
	require.Equal(t, "en", rec.started[0].Language)
	require.True(t, rec.started[0].IsMature)
	require.Equal(t, time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC), rec.started[0].StartedAt)
	require.Nil(t, rec.started[1].CategoryID)
	require.Equal(t, "vi", rec.started[1].Language)
	require.Equal(t, []string{}, rec.started[1].Tags)
	require.False(t, rec.started[1].StartedAt.IsZero())
	require.Nil(t, rec.updated[0].CategoryID, "a malformed category id is dropped")
	require.Equal(t, []string{ch + "/" + st}, rec.ended)
	require.Equal(t, st, rec.scheduled[0].StreamID)
	require.True(t, rec.vods[0].Public)
	require.False(t, rec.vods[1].Public, "hidden VODs leave the search index")
	require.Equal(t, []string{vod}, rec.deleted)
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
		{name: "bad schedule id", e: eventbus.Event{ID: id, Type: pkgevents.TypeStreamScheduled, Payload: []byte(`{"streamId":"` + user + `","scheduleId":"x"}`)}},
		{name: "bad vod id", e: eventbus.Event{ID: id, Type: pkgevents.TypeVODDeleted, Payload: []byte(`{"vodId":"x"}`)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recSync{}
			err := events.Handler(rec, nil)(context.Background(), tc.e)
			require.ErrorIs(t, err, eventbus.ErrSkip)
			require.True(t, rec.empty())
		})
	}
}

type recSafety struct {
	got []pkgevents.ChannelActioned
}

func (r *recSafety) ChannelActioned(_ context.Context, _ string, e pkgevents.ChannelActioned) error {
	r.got = append(r.got, e)

	return nil
}

func TestChannelActioned(t *testing.T) {
	t.Parallel()

	good := pkgevents.ChannelActioned{ActionID: uuid.NewString(), ChannelID: uuid.NewString(), Action: pkgevents.ActionSuspend, Reason: "spam"}

	for _, tc := range []struct {
		name string
		id   string
		p    pkgevents.ChannelActioned
		skip bool
	}{
		{name: "suspend", id: uuid.NewString(), p: good},
		{name: "unknown action", id: uuid.NewString(), p: pkgevents.ChannelActioned{ActionID: good.ActionID, ChannelID: good.ChannelID, Action: "BAN"}, skip: true},
		{name: "no event id", p: good, skip: true},
		{name: "bad channel", id: uuid.NewString(), p: pkgevents.ChannelActioned{ActionID: good.ActionID, ChannelID: "x", Action: pkgevents.ActionRestore}, skip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recSafety{}
			err := events.Handler(&recSync{}, rec)(context.Background(), eventbus.Event{
				ID: tc.id, Type: pkgevents.TypeChannelActioned, Payload: mustJSON(t, tc.p),
			})

			if tc.skip {
				require.ErrorIs(t, err, eventbus.ErrSkip)
				require.Empty(t, rec.got)

				return
			}

			require.NoError(t, err)
			require.Len(t, rec.got, 1)
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)

	return b
}
