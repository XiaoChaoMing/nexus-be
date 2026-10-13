package playback_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/playback"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

// fakeStreams: "s-plain" and "s-mature" exist; VOD "v-mature" records "s-mature", "v-plain" records "s-plain".
type fakeStreams struct{ repo.StreamRepo }

func (fakeStreams) Get(_ context.Context, id string) (entity.Stream, error) {
	switch id {
	case "s-plain":
		return entity.Stream{ID: id, ChannelID: "ch1"}, nil
	case "s-mature":
		return entity.Stream{ID: id, ChannelID: "ch1", IsMature: true}, nil
	}

	return entity.Stream{}, entity.ErrNotFound
}

func (fakeStreams) GetVOD(_ context.Context, id string) (entity.VOD, error) {
	switch id {
	case "v-plain":
		return entity.VOD{ID: id, StreamID: "s-plain", ChannelID: "ch1", PlaylistPath: "streams/s-plain/vod.m3u8", DurationSec: 600}, nil
	case "v-mature":
		return entity.VOD{ID: id, StreamID: "s-mature", ChannelID: "ch1", PlaylistPath: "streams/s-mature/vod.m3u8"}, nil
	}

	return entity.VOD{}, entity.ErrNotFound
}

type fakeChannels struct{ repo.ChannelRepo }

func (fakeChannels) ChannelsByID(_ context.Context, _ []string) (map[string]entity.Channel, error) {
	return map[string]entity.Channel{"ch1": {ID: "ch1", Handle: "minhnn"}}, nil
}

type fakeViewers struct {
	sessions []entity.PlaybackSession
	consents map[string]bool
	progress []entity.WatchProgress
	keep     []bool
	err      error
	purged   time.Time
}

func newViewers() *fakeViewers { return &fakeViewers{consents: map[string]bool{}} }

func (f *fakeViewers) CreatePlaybackSession(_ context.Context, s entity.PlaybackSession) error {
	if f.err != nil {
		return f.err
	}

	f.sessions = append(f.sessions, s)

	return nil
}

func (f *fakeViewers) HasMatureConsent(_ context.Context, userID, channelID string) (bool, error) {
	return f.consents[userID+"/"+channelID], f.err
}

func (f *fakeViewers) SaveMatureConsent(_ context.Context, userID, channelID string) error {
	if f.err != nil {
		return f.err
	}

	f.consents[userID+"/"+channelID] = true

	return nil
}

func (f *fakeViewers) SaveProgress(_ context.Context, p entity.WatchProgress, keepFurther bool) error {
	f.progress = append(f.progress, p)
	f.keep = append(f.keep, keepFurther)

	return f.err
}

func (*fakeViewers) Progress(context.Context, string, string) (*int, error) { return nil, nil } //nolint:nilnil // never watched

func (*fakeViewers) ContinueWatching(_ context.Context, _ string, limit int) ([]entity.ContinueItem, error) {
	out := []entity.ContinueItem{{VOD: entity.VOD{ID: "v-plain", ChannelID: "ch1"}, PositionSec: 30}}
	if limit < len(out) {
		out = out[:limit]
	}

	return out, nil
}

func (f *fakeViewers) PurgePlaybackSessions(_ context.Context, before time.Time) (int64, error) {
	f.purged = before

	return 3, nil
}

var _now = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test clock

func newUC(v *fakeViewers) *playback.UseCase {
	return playback.New(fakeStreams{}, fakeChannels{}, v, logger.New("error")).WithClock(func() time.Time { return _now })
}

// _generated marks a test case where the use case must invent the guest id.
const _generated = "generated"

type openCase struct {
	name        string
	in          usecase.PlaybackInput
	consent     bool
	wantOpen    bool
	wantMature  bool
	wantKey     string
	wantAnon    string
	wantConsent bool
}

func openCases() []openCase {
	return []openCase{
		{
			name: "guest, plain stream", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-plain"}},
			wantOpen: true, wantKey: "streams/s-plain/master.m3u8", wantAnon: _generated,
		},
		{
			name: "guest keeps its id", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-plain"}, AnonID: "a1"},
			wantOpen: true, wantKey: "streams/s-plain/master.m3u8", wantAnon: "a1",
		},
		{
			name: "guest, mature, not acknowledged", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-mature"}, AnonID: "a1"},
			wantMature: true, wantAnon: "a1",
		},
		{
			name: "guest, mature, acknowledged (not remembered)", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-mature"}, AnonID: "a1", AcknowledgeMature: true},
			wantOpen: true, wantMature: true, wantKey: "streams/s-mature/master.m3u8", wantAnon: "a1",
		},
		{
			name: "user acknowledges: remembered", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-mature"}, UserID: "u1", AcknowledgeMature: true},
			wantOpen: true, wantMature: true, wantKey: "streams/s-mature/master.m3u8", wantConsent: true,
		},
		{
			name: "user with consent is not asked", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-mature"}, UserID: "u1"}, consent: true,
			wantOpen: true, wantMature: true, wantKey: "streams/s-mature/master.m3u8", wantConsent: true,
		},
		{
			name: "user without consent is asked", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-mature"}, UserID: "u1"},
			wantMature: true,
		},
		{
			name: "vod inherits mature flag", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{VODID: "v-mature"}, UserID: "u1"},
			wantMature: true,
		},
		{
			name: "plain vod", in: usecase.PlaybackInput{Target: entity.PlaybackTarget{VODID: "v-plain"}, UserID: "u1"},
			wantOpen: true, wantKey: "streams/s-plain/vod.m3u8",
		},
	}
}

func TestOpen(t *testing.T) {
	t.Parallel()

	for _, tc := range openCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := newViewers()
			if tc.consent {
				v.consents["u1/ch1"] = true
			}

			g, err := newUC(v).Open(context.Background(), tc.in)
			require.NoError(t, err)
			checkOpen(t, tc, g, v)
		})
	}
}

func checkOpen(t *testing.T, tc openCase, g entity.PlaybackGrant, v *fakeViewers) {
	t.Helper()

	assert.Equal(t, tc.wantMature, g.Mature.Required)
	assert.Equal(t, tc.wantOpen, g.Mature.Open())
	assert.Equal(t, tc.wantKey, g.ObjectKey)
	assert.Equal(t, tc.wantConsent, v.consents["u1/ch1"])

	switch tc.wantAnon {
	case "":
		assert.Empty(t, g.AnonID)
	case _generated:
		assert.Len(t, g.AnonID, 36)
	default:
		assert.Equal(t, tc.wantAnon, g.AnonID)
	}

	if !tc.wantOpen {
		assert.Nil(t, g.Session)
		assert.Empty(t, v.sessions, "no session while the gate is closed")

		return
	}

	require.NotNil(t, g.Session)
	require.Len(t, v.sessions, 1)
	s := v.sessions[0]
	assert.Equal(t, tc.in.Target, s.Target)
	assert.Equal(t, tc.in.UserID, s.UserID)
	assert.Equal(t, g.AnonID, s.AnonID)
	assert.Equal(t, tc.wantMature, s.MatureAcknowledged)
	assert.Equal(t, _now.Add(entity.PlaybackTTL), s.ExpiresAt)
}

func TestOpen_Errors(t *testing.T) {
	t.Parallel()

	_, err := newUC(newViewers()).Open(context.Background(), usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "nope"}})
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, err = newUC(newViewers()).Open(context.Background(), usecase.PlaybackInput{Target: entity.PlaybackTarget{VODID: "nope"}})
	require.ErrorIs(t, err, entity.ErrNotFound)

	v := newViewers()
	v.err = errDB
	_, err = newUC(v).Open(context.Background(), usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-plain"}})
	require.ErrorIs(t, err, errDB)

	_, err = newUC(v).Open(context.Background(), usecase.PlaybackInput{Target: entity.PlaybackTarget{StreamID: "s-mature"}, UserID: "u1"})
	require.ErrorIs(t, err, errDB)
}

func TestSaveProgress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		vod     string
		pos     int
		wantErr error
	}{
		{name: "inside", vod: "v-plain", pos: 120},
		{name: "a little past the end is tolerated", vod: "v-plain", pos: 604},
		{name: "unknown duration", vod: "v-mature", pos: 99999},
		{name: "negative", vod: "v-plain", pos: -1, wantErr: entity.ErrInvalidPosition},
		{name: "beyond the end", vod: "v-plain", pos: 700, wantErr: entity.ErrInvalidPosition},
		{name: "unknown vod", vod: "nope", pos: 1, wantErr: entity.ErrNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := newViewers()
			err := newUC(v).SaveProgress(context.Background(), entity.WatchProgress{UserID: "u1", VODID: tc.vod, PositionSec: tc.pos}, true)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Empty(t, v.progress)

				return
			}

			require.NoError(t, err)
			require.Len(t, v.progress, 1)
			assert.Equal(t, _now, v.progress[0].UpdatedAt)
			assert.True(t, v.keep[0])
		})
	}
}

func TestContinueWatchingAndPurge(t *testing.T) {
	t.Parallel()

	v := newViewers()
	uc := newUC(v)

	items, err := uc.ContinueWatching(context.Background(), "u1", 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].VOD.Channel)
	assert.Equal(t, "minhnn", items[0].VOD.Channel.Handle)

	n, err := uc.PurgeSessions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.Equal(t, _now.Add(-24*time.Hour), v.purged)
}
