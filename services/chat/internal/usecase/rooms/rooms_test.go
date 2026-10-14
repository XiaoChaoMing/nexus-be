package rooms_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/rooms"
	"github.com/stretchr/testify/require"
)

const (
	stream  = "0199d4a2-0000-7000-8000-0000000000aa"
	channel = "0199d4a2-0000-7000-8000-0000000000cc"
)

type fakeRooms struct {
	repo.RoomRepo
	open        map[string]bool
	closedPeaks []int
}

func (f *fakeRooms) OpenRoom(_ context.Context, r entity.Room) (bool, error) {
	if f.open[r.StreamID] {
		return false, nil
	}

	f.open[r.StreamID] = true

	return true, nil
}

func (f *fakeRooms) CloseRoom(_ context.Context, streamID string, peak int) (bool, error) {
	if !f.open[streamID] {
		return false, nil
	}

	f.open[streamID] = false
	f.closedPeaks = append(f.closedPeaks, peak)

	return true, nil
}

type fakePresence struct {
	repo.PresenceStore
	status map[string]string
	peak   int
}

func (f *fakePresence) SetStreamStatus(_ context.Context, id, s string) error {
	f.status[id] = s

	return nil
}

func (f *fakePresence) Peak(context.Context, string) (int, error) { return f.peak, nil }

type fakeMessages struct {
	repo.MessageRepo
	from    time.Time
	days    int
	dropped time.Time
}

func (f *fakeMessages) EnsurePartitions(_ context.Context, from time.Time, days int) ([]string, error) {
	f.from, f.days = from, days

	return []string{"chat_messages_x"}, nil
}

func (f *fakeMessages) DropPartitionsBefore(_ context.Context, day time.Time) ([]string, error) {
	f.dropped = day

	return nil, nil
}

type pub struct{ got []string }

func (p *pub) Publish(_ context.Context, topic entity.Topic, typ string, data any) (entity.Event, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return entity.Event{}, err
	}

	p.got = append(p.got, topic.String()+" "+typ+" "+string(b))

	return entity.Event{}, nil
}

type fixture struct {
	uc       *rooms.UseCase
	rooms    *fakeRooms
	presence *fakePresence
	messages *fakeMessages
	pub      *pub
}

func newFixture() fixture {
	f := fixture{
		rooms: &fakeRooms{open: map[string]bool{}}, presence: &fakePresence{status: map[string]string{}, peak: 4},
		messages: &fakeMessages{}, pub: &pub{},
	}
	f.uc = rooms.New(rooms.Repos{Rooms: f.rooms, Messages: f.messages, Presence: f.presence}, f.pub, 720*time.Hour, logger.New("error"))

	return f
}

func TestStreamLifecycleRelays(t *testing.T) {
	t.Parallel()

	f := newFixture()
	uc, r, pr, p := f.uc, f.rooms, f.presence, f.pub
	ctx := context.Background()

	require.NoError(t, uc.StreamStarted(ctx, events.StreamStarted{StreamID: stream, ChannelID: channel}))
	require.Equal(t, "LIVE", pr.status[stream])

	require.NoError(t, uc.StreamStatusChanged(ctx, events.StreamStatusChanged{StreamID: stream, Status: "INTERRUPTED"}))
	require.NoError(t, uc.StreamUpdated(ctx, events.StreamUpdated{StreamID: stream, Title: "New"}))
	require.NoError(t, uc.StreamEnded(ctx, events.StreamEnded{StreamID: stream, EndReason: "creator"}))
	require.NoError(t, uc.StreamEnded(ctx, events.StreamEnded{StreamID: stream, EndReason: "creator"}), "redelivery")
	require.NoError(t, uc.StreamVODReady(ctx, events.StreamVODReady{StreamID: stream, VODID: "v1"}))

	require.Equal(t, []int{4}, r.closedPeaks, "the peak is reported once")
	require.Equal(t, "ENDED", pr.status[stream])
	require.Equal(t, []string{
		"chat:" + stream + ` room_state {"status":"OPEN"}`,
		"stream:" + stream + ` status_changed {"status":"LIVE"}`,
		"stream:" + stream + ` status_changed {"status":"INTERRUPTED"}`,
		"stream:" + stream + ` title_changed {"title":"New","tags":[]}`,
		"chat:" + stream + ` room_state {"status":"READ_ONLY"}`,
		"stream:" + stream + ` status_changed {"status":"ENDED","endReason":"creator"}`,
		"chat:" + stream + ` room_state {"status":"READ_ONLY"}`,
		"stream:" + stream + ` status_changed {"status":"ENDED","endReason":"creator"}`,
		"stream:" + stream + ` vod_ready {"vodId":"v1"}`,
	}, p.got)
}

func TestMaintainPartitions(t *testing.T) {
	t.Parallel()

	f := newFixture()
	m := f.messages

	require.NoError(t, f.uc.MaintainPartitions(context.Background()))
	require.Equal(t, 4, m.days, "today and three days ahead")
	require.Equal(t, m.from.Add(-720*time.Hour), m.dropped)
}
