package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/chat"
	"github.com/stretchr/testify/require"
)

const (
	streamID  = "0199d4a2-0000-7000-8000-0000000000aa"
	channelID = "0199d4a2-0000-7000-8000-0000000000cc"
	owner     = "0199d4a2-0000-7000-8000-000000000001"
	viewer    = "0199d4a2-0000-7000-8000-000000000002"
	other     = "0199d4a2-0000-7000-8000-000000000003"
)

type recObserver struct {
	sent    int
	rejects []string
}

func (o *recObserver) MessageSent()         { o.sent++ }
func (o *recObserver) Rejected(code string) { o.rejects = append(o.rejects, code) }

func defaultConfig() chat.Config {
	return chat.Config{RateLimit: 3, RateLimitPrivileged: 10, RateLimitIP: 100, RateWindow: 30 * time.Second, MaxLines: 3, HistoryDefault: 50}
}

func newUC(w *world, cfg chat.Config) (*chat.UseCase, *recObserver) {
	obs := &recObserver{}
	uc := chat.New(chat.Repos{
		Rooms: fakeRooms{w: w}, Messages: fakeMessages{w: w}, Settings: fakeSettings{w: w}, Roles: fakeRoles{w: w},
		Emotes: fakeEmotes{}, Users: fakeUsers{w: w}, Guard: fakeGuard{w: w}, Audience: fakeAudience{w: w},
	}, fakePub{w: w}, cfg, logger.New("error"), chat.WithObserver(obs), chat.WithClock(w.clock))

	return uc, obs
}

func user(id string) entity.Principal {
	return entity.Principal{UserID: id, ConnID: "c-" + id, IP: "10.0.0.1"}
}

func msg(body string) entity.SendInput {
	return entity.SendInput{ClientMsgID: uuid.NewString(), Body: body}
}

func TestSendHappyPath(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.ownerID = owner
	w.users[owner] = entity.User{ID: owner, Username: "minhnn", DisplayName: "Minh"}
	w.users[viewer] = entity.User{ID: viewer, Username: "viewer_a"}
	uc, obs := newUC(w, defaultConfig())

	res, err := uc.Send(context.Background(), user(owner), streamID, msg("  gg nxGG @Viewer_A  "))
	require.NoError(t, err)
	require.False(t, res.Duplicate)

	m := res.Message
	require.Equal(t, "gg nxGG @Viewer_A", m.Body)
	require.Equal(t, []string{entity.BadgeStreamer}, m.User.Badges)
	require.Equal(t, "Minh", m.User.DisplayName)
	require.Equal(t, entity.ColorOf(owner), m.User.Color)
	require.Equal(t, []entity.Fragment{
		{Type: "text", Text: "gg "},
		{Type: "emote", Text: "nxGG", Emote: &entity.EmoteRef{ID: "e1", Code: "nxGG", ImageURL: "/emotes/nxGG.webp"}},
		{Type: "text", Text: " "},
		{Type: "mention", Text: "@Viewer_A", UserID: viewer},
	}, m.Fragments)
	require.True(t, m.MentionsUser(viewer))

	require.Len(t, w.published, 1)
	require.Equal(t, "chat:"+streamID, w.published[0].topic)
	require.Equal(t, entity.EventMessage, w.published[0].typ)
	require.Equal(t, 1, obs.sent)
}

func TestSendRetryWithSameClientMsgID(t *testing.T) {
	t.Parallel()

	w := newWorld()
	uc, obs := newUC(w, defaultConfig())

	in := msg("hello")
	first, err := uc.Send(context.Background(), user(viewer), streamID, in)
	require.NoError(t, err)

	again, err := uc.Send(context.Background(), user(viewer), streamID, in)
	require.NoError(t, err)
	require.True(t, again.Duplicate)
	require.Equal(t, first.Message.ID, again.Message.ID)
	require.Len(t, w.inserted, 1, "a retry never stores a second message")
	require.Len(t, w.published, 1)
	require.Equal(t, 1, obs.sent)
}

// step is one send in a scenario; wantErr nil = accepted.
type step struct {
	who     string
	body    string
	wait    time.Duration
	wantErr error
	details map[string]string
}

func TestSendRules(t *testing.T) { //nolint:funlen // one table of every rule
	t.Parallel()

	ten, sixty := 10, 60

	tests := []struct {
		name  string
		cfg   func(*chat.Config)
		setup func(*world)
		steps []step
	}{
		{name: "guest", steps: []step{{who: "", body: "hi", wantErr: entity.ErrUnauthenticated}}},
		{name: "kill switch", cfg: func(c *chat.Config) { c.Disabled = true }, steps: []step{{who: viewer, body: "hi", wantErr: entity.ErrChatDisabled}}},
		{name: "empty body", steps: []step{{who: viewer, body: " \n ", wantErr: entity.ErrValidation, details: map[string]string{"body": "REQUIRED"}}}},
		{name: "501 characters", steps: []step{{who: viewer, body: strings.Repeat("a", 501), wantErr: entity.ErrValidation, details: map[string]string{"body": "LENGTH"}}}},
		{name: "four lines", steps: []step{{who: viewer, body: "a\nb\nc\nd", wantErr: entity.ErrValidation, details: map[string]string{"body": "LENGTH"}}}},
		{name: "no room", setup: func(w *world) { w.room = nil }, steps: []step{{who: viewer, body: "hi", wantErr: entity.ErrRoomClosed}}},
		{
			name: "read only", setup: func(w *world) { w.room.Status = entity.RoomReadOnly },
			steps: []step{{who: viewer, body: "hi", wantErr: entity.ErrRoomReadOnly}},
		},
		{
			name: "banned, but not the streamer", setup: func(w *world) { w.ownerID = owner; w.banned[viewer], w.banned[owner] = true, true },
			steps: []step{{who: viewer, body: "hi", wantErr: entity.ErrBanned}, {who: owner, body: "hi"}},
		},
		{
			name: "timed out", setup: func(w *world) { w.timeouts[viewer] = w.now.Add(time.Minute) },
			steps: []step{{who: viewer, body: "hi", wantErr: entity.ErrTimedOut, details: map[string]string{"until": "2026-10-14T12:01:00Z"}}},
		},
		{
			name: "verified e-mail required", cfg: func(c *chat.Config) { c.RequireVerified = true },
			setup: func(w *world) { w.users[other] = entity.User{ID: other, Username: "ok", EmailVerified: true} },
			steps: []step{{who: viewer, body: "hi", wantErr: entity.ErrEmailNotVerified}, {who: other, body: "hi"}},
		},
		{
			name: "followers only: not following, too recent, long enough, moderator exempt",
			setup: func(w *world) {
				w.settings.FollowersMin = &ten
				w.follows[other] = w.now.Add(-5 * time.Minute)
				w.follows[owner] = w.now.Add(-time.Hour)
				w.roles["0199d4a2-0000-7000-8000-000000000004"] = entity.ChannelRoleModerator
			},
			steps: []step{
				{who: viewer, body: "hi", wantErr: entity.ErrFollowersOnly, details: map[string]string{"requiredMin": "10"}},
				{who: other, body: "hi", wantErr: entity.ErrFollowersOnly, details: map[string]string{"requiredMin": "10", "followedMin": "5"}},
				{who: owner, body: "hi"},
				{who: "0199d4a2-0000-7000-8000-000000000004", body: "hi"},
			},
		},
		{
			name: "subs only: viewer and VIP refused, subscriber and streamer exempt",
			setup: func(w *world) {
				w.settings.SubsOnly = true
				w.ownerID = owner
				w.tiers[other] = 2
				w.roles[viewer] = entity.ChannelRoleVIP
			},
			steps: []step{
				{who: viewer, body: "hi", wantErr: entity.ErrSubsOnly},
				{who: other, body: "hi"},
				{who: owner, body: "hi"},
			},
		},
		{
			name: "modes combine: followers and subs",
			setup: func(w *world) {
				w.settings.FollowersMin, w.settings.SubsOnly = &sixty, true
				w.tiers[viewer] = 1
				w.follows[other] = w.now.Add(-2 * time.Hour)
			},
			steps: []step{
				{who: viewer, body: "hi", wantErr: entity.ErrFollowersOnly, details: map[string]string{"requiredMin": "60"}},
				{who: other, body: "hi", wantErr: entity.ErrSubsOnly},
			},
		},
		{
			name: "slow mode: second message refused until it expires, VIP exempt",
			setup: func(w *world) {
				w.settings.SlowSec = 10
				w.roles[other] = entity.ChannelRoleVIP
			},
			steps: []step{
				{who: viewer, body: "one"},
				{who: viewer, body: "two", wait: 3 * time.Second, wantErr: entity.ErrSlowMode, details: map[string]string{"retryAfterSec": "7"}},
				{who: viewer, body: "three", wait: 8 * time.Second},
				{who: other, body: "a"},
				{who: other, body: "b"},
			},
		},
		{
			name: "duplicate body inside the window",
			steps: []step{
				{who: viewer, body: "same"},
				{who: viewer, body: " same ", wantErr: entity.ErrDuplicate},
				{who: viewer, body: "different"},
				{who: other, body: "same"},
			},
		},
		{
			name:  "rate limit per user, higher ceiling for the streamer",
			setup: func(w *world) { w.ownerID = owner },
			steps: []step{
				{who: viewer, body: "1"},
				{who: viewer, body: "2"},
				{who: viewer, body: "3"},
				{who: viewer, body: "4", wantErr: entity.ErrRateLimited, details: map[string]string{"retryAfterSec": "2"}},
				{who: owner, body: "1"},
				{who: owner, body: "2"},
				{who: owner, body: "3"},
				{who: owner, body: "4"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			if tc.setup != nil {
				tc.setup(w)
			}

			cfg := defaultConfig()
			if tc.cfg != nil {
				tc.cfg(&cfg)
			}

			uc, obs := newUC(w, cfg)
			refused := runSteps(t, uc, w, tc.steps)

			require.Len(t, obs.rejects, refused)
			require.Len(t, w.inserted, len(tc.steps)-refused)
		})
	}
}

// runSteps sends each step and checks its outcome; it returns how many were refused.
func runSteps(t *testing.T, uc *chat.UseCase, w *world, steps []step) int {
	t.Helper()

	refused := 0

	for i, s := range steps {
		w.advance(s.wait)

		p := entity.Principal{IP: "10.0.0.1"}
		if s.who != "" {
			p = user(s.who)
		}

		_, err := uc.Send(context.Background(), p, streamID, msg(s.body))
		if s.wantErr == nil {
			require.NoError(t, err, "step %d", i)

			continue
		}

		refused++

		require.ErrorIs(t, err, s.wantErr, "step %d", i)
		require.Equal(t, s.details, entity.DetailsOf(err), "step %d", i)
	}

	return refused
}

func TestSendRejectsBadClientMsgID(t *testing.T) {
	t.Parallel()

	uc, _ := newUC(newWorld(), defaultConfig())

	_, err := uc.Send(context.Background(), user(viewer), streamID, entity.SendInput{ClientMsgID: "x", Body: "hi"})
	require.ErrorIs(t, err, entity.ErrValidation)
	require.Equal(t, map[string]string{"clientMsgId": "FORMAT"}, entity.DetailsOf(err))
}

func TestSnapshot(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.ownerID = owner
	w.settings.SlowSec = 5
	uc, _ := newUC(w, defaultConfig())
	ctx := context.Background()

	guest, err := uc.Snapshot(ctx, entity.Principal{ConnID: "c"}, streamID)
	require.NoError(t, err)
	require.Equal(t, entity.RoomOpen, guest.Room.Status)
	require.Equal(t, 5, guest.Room.Mode.SlowSec)
	require.False(t, guest.Me.CanSend)
	require.Equal(t, "UNAUTHORIZED", *guest.Me.Reason)

	streamer, err := uc.Snapshot(ctx, user(owner), streamID)
	require.NoError(t, err)
	require.True(t, streamer.Me.CanSend)
	require.Equal(t, "STREAMER", streamer.Me.Role)
	require.Equal(t, []string{"STREAMER"}, streamer.Me.Badges)

	_, err = uc.Send(ctx, user(viewer), streamID, msg("hi"))
	require.NoError(t, err)

	slowed, err := uc.Snapshot(ctx, user(viewer), streamID)
	require.NoError(t, err)
	require.False(t, slowed.Me.CanSend)
	require.Equal(t, "CHAT_SLOW_MODE", *slowed.Me.Reason)
	require.Equal(t, 5, *slowed.Me.RetryAfterSec)

	unknown, err := uc.Snapshot(ctx, user(viewer), uuid.NewString())
	require.NoError(t, err)
	require.Equal(t, entity.RoomNotOpen, unknown.Room.Status)
	require.Equal(t, "CHAT_CLOSED", *unknown.Me.Reason)
}

func TestUpdateSettings(t *testing.T) {
	t.Parallel()

	mod := "0199d4a2-0000-7000-8000-000000000004"
	ten := 10

	tests := []struct {
		name    string
		p       entity.Principal
		in      entity.ChatSettings
		wantErr error
	}{
		{name: "guest", p: entity.Principal{}, in: entity.ChatSettings{SlowSec: 5}, wantErr: entity.ErrUnauthenticated},
		{name: "viewer", p: user(viewer), in: entity.ChatSettings{SlowSec: 5}, wantErr: entity.ErrForbidden},
		{name: "bad enum", p: user(owner), in: entity.ChatSettings{SlowSec: 7}, wantErr: entity.ErrValidation},
		{name: "owner", p: user(owner), in: entity.ChatSettings{SlowSec: 5, FollowersMin: &ten}},
		{name: "moderator", p: user(mod), in: entity.ChatSettings{SubsOnly: true}},
		{name: "admin", p: entity.Principal{UserID: other, GlobalRole: "ADMIN"}, in: entity.ChatSettings{SlowSec: 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.ownerID = owner
			w.roles[mod] = entity.ChannelRoleModerator
			w.openRooms = []string{streamID}
			uc, _ := newUC(w, defaultConfig())

			tc.in.ChannelID = channelID

			got, err := uc.UpdateSettings(context.Background(), tc.p, tc.in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Empty(t, w.published)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.p.UserID, *got.UpdatedBy)
			require.NotNil(t, got.UpdatedAt)
			require.Len(t, w.published, 2, "mode_change + system message")
			require.Equal(t, entity.EventModeChange, w.published[0].typ)
			require.Equal(t, entity.EventSystem, w.published[1].typ)
		})
	}
}

func TestUpdateSettingsUnchangedIsQuiet(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.ownerID = owner
	w.openRooms = []string{streamID}
	uc, _ := newUC(w, defaultConfig())

	_, err := uc.UpdateSettings(context.Background(), user(owner), entity.ChatSettings{ChannelID: channelID})
	require.NoError(t, err)
	require.Empty(t, w.published)
}

func TestHistoryAndSettings(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.ownerID = owner
	w.tiers[viewer] = 3
	w.users[owner] = entity.User{ID: owner, Username: "minhnn", DisplayName: "Minh"}
	uc, _ := newUC(w, defaultConfig())
	ctx := context.Background()

	for _, s := range []struct{ who, body string }{{owner, "a"}, {viewer, "b"}, {owner, "c"}} {
		_, err := uc.Send(ctx, user(s.who), streamID, msg(s.body))
		require.NoError(t, err)
	}

	page, next, err := uc.History(ctx, streamID, nil, 2)
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.NotNil(t, next)
	require.Equal(t, "c", page[0].Body)
	require.Equal(t, []string{"STREAMER"}, page[0].User.Badges)
	require.Equal(t, []string{"SUB_T3"}, page[1].User.Badges)
	require.Equal(t, entity.ColorOf(viewer), page[1].User.Color)

	_, _, err = uc.History(ctx, uuid.NewString(), nil, 2)
	require.ErrorIs(t, err, entity.ErrNotFound)

	s, err := uc.Settings(ctx, channelID)
	require.NoError(t, err)
	require.Nil(t, s.UpdatedAt)

	_, err = uc.Settings(ctx, uuid.NewString())
	require.ErrorIs(t, err, entity.ErrNotFound)

	emotes, err := uc.Emotes(ctx)
	require.NoError(t, err)
	require.Len(t, emotes, 1)
}

type terms []string

func (t terms) BlockedTerms(context.Context, string) ([]string, error) { return t, nil }

func TestSendAutoMod(t *testing.T) {
	t.Parallel()

	vip := "0199d4a2-0000-7000-8000-000000000005"
	mod := "0199d4a2-0000-7000-8000-000000000006"

	tests := []struct {
		name   string
		sender string
		body   string
		reason string
	}{
		{name: "blocked term", sender: viewer, body: "SPẢM nè", reason: entity.BlockReasonTerm},
		{name: "link", sender: viewer, body: "xem tại abc.xyz", reason: entity.BlockReasonLink},
		{name: "vip link", sender: vip, body: "xem tại abc.xyz"},
		{name: "vip term", sender: vip, body: "spam", reason: entity.BlockReasonTerm},
		{name: "moderator", sender: mod, body: "spam abc.xyz"},
		{name: "owner", sender: owner, body: "spam abc.xyz"},
		{name: "clean", sender: viewer, body: "spammer hello"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.ownerID = owner
			w.settings.BlockLinks = true
			w.roles[vip] = entity.ChannelRoleVIP
			w.roles[mod] = entity.ChannelRoleModerator
			uc := chat.New(chat.Repos{
				Rooms: fakeRooms{w: w}, Messages: fakeMessages{w: w}, Settings: fakeSettings{w: w}, Roles: fakeRoles{w: w},
				Emotes: fakeEmotes{}, Users: fakeUsers{w: w}, Guard: fakeGuard{w: w}, Audience: fakeAudience{w: w},
			}, fakePub{w: w}, defaultConfig(), logger.New("error"), chat.WithClock(w.clock), chat.WithAutoMod(terms{"spam"}))

			_, err := uc.Send(context.Background(), user(tc.sender), streamID, msg(tc.body))
			if tc.reason == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, entity.ErrContentBlocked)
			require.Equal(t, map[string]string{"reason": tc.reason}, entity.DetailsOf(err))
			require.Empty(t, w.inserted)
			require.Equal(t, 1, w.buckets["chat:rate:"+tc.sender], "a blocked message still costs its rate token")
		})
	}
}

func TestUpdateSettingsQueuesChange(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.ownerID = owner
	uc, _ := newUC(w, defaultConfig())

	_, err := uc.UpdateSettings(context.Background(), user(owner), entity.ChatSettings{ChannelID: channelID, BlockLinks: true})
	require.NoError(t, err)
	require.Len(t, w.settingsEvents, 1)
	require.Equal(t, "OWNER", w.settingsEvents[0].ActorRole)
	require.True(t, w.settingsEvents[0].After.BlockLinks)
	require.False(t, w.settingsEvents[0].Before.BlockLinks)

	_, err = uc.UpdateSettings(context.Background(), user(owner), entity.ChatSettings{ChannelID: channelID, BlockLinks: true})
	require.NoError(t, err)
	require.Len(t, w.settingsEvents, 1, "no change, no event")
}
