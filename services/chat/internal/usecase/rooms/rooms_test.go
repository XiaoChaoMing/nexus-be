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
	online map[string]bool
}

func (f *fakePresence) Online(_ context.Context, ids []string) ([]string, error) {
	out := []string{}

	for _, id := range ids {
		if f.online[id] {
			out = append(out, id)
		}
	}

	return out, nil
}

type fakeAudience struct {
	repo.AudienceStore
	followers []string
}

func (f *fakeAudience) Followers(context.Context, string) ([]string, error) { return f.followers, nil }

type fakeLive struct{ live map[string]entity.LiveInfo }

func (f *fakeLive) SetLive(_ context.Context, ch string, l entity.LiveInfo) error {
	f.live[ch] = l

	return nil
}

func (f *fakeLive) ClearLive(_ context.Context, ch, streamID string) error {
	if f.live[ch].StreamID == streamID {
		delete(f.live, ch)
	}

	return nil
}

func (f *fakeLive) Live(_ context.Context, ids []string) (map[string]entity.LiveInfo, error) {
	out := map[string]entity.LiveInfo{}

	for _, id := range ids {
		if l, ok := f.live[id]; ok {
			out[id] = l
		}
	}

	return out, nil
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
	audience *fakeAudience
	live     *fakeLive
	pub      *pub
}

func newFixture() fixture {
	f := fixture{
		rooms: &fakeRooms{open: map[string]bool{}}, presence: &fakePresence{status: map[string]string{}, peak: 4, online: map[string]bool{}},
		messages: &fakeMessages{}, audience: &fakeAudience{}, live: &fakeLive{live: map[string]entity.LiveInfo{}}, pub: &pub{},
	}
	f.uc = rooms.New(rooms.Repos{
		Rooms: f.rooms, Messages: f.messages, Presence: f.presence, Audience: f.audience, Live: f.live,
	}, f.pub, 720*time.Hour, logger.New("error"))

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

func TestSidebarFanOut(t *testing.T) {
	t.Parallel()

	const (
		online  = "0199d4a2-0000-7000-8000-0000000000a1"
		offline = "0199d4a2-0000-7000-8000-0000000000a2"
		newer   = "0199d4a2-0000-7000-8000-0000000000bb"
	)

	f := newFixture()
	f.audience.followers = []string{online, offline}
	f.presence.online[online] = true
	ctx := context.Background()
	cat := "0199d4a2-0000-7000-8000-0000000000ca"

	require.NoError(t, f.uc.StreamStarted(ctx, events.StreamStarted{StreamID: stream, ChannelID: channel, Title: "Rank", CategoryID: &cat}))
	require.Equal(t, stream, f.live.live[channel].StreamID)

	require.NoError(t, f.uc.StreamUpdated(ctx, events.StreamUpdated{StreamID: stream, ChannelID: channel, Title: "Rank 2"}))
	require.Equal(t, "Rank 2", f.live.live[channel].Title)

	// A late stream.ended of an older stream leaves the newer one live.
	f.live.live[channel] = entity.LiveInfo{StreamID: newer}
	require.NoError(t, f.uc.StreamEnded(ctx, events.StreamEnded{StreamID: stream, ChannelID: channel}))
	require.Equal(t, newer, f.live.live[channel].StreamID)

	f.live.live[channel] = entity.LiveInfo{StreamID: stream}
	require.NoError(t, f.uc.StreamEnded(ctx, events.StreamEnded{StreamID: stream, ChannelID: channel}))
	require.Empty(t, f.live.live)

	var sidebar []string

	for _, g := range f.pub.got {
		if len(g) > 8 && g[:8] == "sidebar:" {
			sidebar = append(sidebar, g)
		}
	}

	require.Len(t, sidebar, 3, "only the follower who is online")
	require.Equal(t, "sidebar:"+online+` channel_live {"channelId":"`+channel+`","stream":{"id":"`+stream+
		`","title":"Rank","categoryId":"`+cat+`","viewerCount":null}}`, sidebar[0])
	require.Contains(t, sidebar[2], "sidebar:"+online+` channel_offline {"channelId":"`+channel+`","streamId":"`+stream+`","lastLiveAt":`)
}

func TestScheduledStatusRelay(t *testing.T) {
	t.Parallel()

	f := newFixture()
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)

	require.NoError(t, f.uc.StreamStatusChanged(ctx, events.StreamStatusChanged{StreamID: stream, Status: "SCHEDULED", ScheduledStartAt: &at}))
	require.NoError(t, f.uc.StreamStatusChanged(ctx, events.StreamStatusChanged{StreamID: stream, Status: "CANCELLED"}))
	require.Equal(t, "CANCELLED", f.presence.status[stream])
	require.Equal(t, []string{
		"stream:" + stream + ` status_changed {"status":"SCHEDULED","scheduledStartAt":"2026-10-20T13:00:00Z"}`,
		"stream:" + stream + ` status_changed {"status":"CANCELLED"}`,
	}, f.pub.got)
}
