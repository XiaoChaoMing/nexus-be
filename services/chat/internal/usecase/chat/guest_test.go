package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/chat"
	"github.com/stretchr/testify/require"
)

const (
	anonA   = "0199d4a2-0000-7000-8000-00000000a0a1"
	anonB   = "0199d4a2-0000-7000-8000-00000000a0a2"
	anonC   = "0199d4a2-0000-7000-8000-00000000a0a3"
	guestIP = "10.0.0.9"
)

func guestKey(anon string) string { return entity.GuestKey([]byte("test-secret"), anon) }

func guest(anon string) entity.Principal {
	return entity.Principal{AnonID: anon, GuestKey: guestKey(anon), ConnID: "c-" + anon, IP: guestIP}
}

// newGuestUC is newUC with the channel's blocked term "spam" (nicknames are checked against it too).
func newGuestUC(w *world, cfg chat.Config) *chat.UseCase {
	return chat.New(chat.Repos{
		Rooms: fakeRooms{w: w}, Messages: fakeMessages{w: w}, Settings: fakeSettings{w: w}, Roles: fakeRoles{w: w},
		Emotes: fakeEmotes{}, Users: fakeUsers{w: w}, Guard: fakeGuard{w: w}, Audience: fakeAudience{w: w},
	}, fakePub{w: w}, cfg, logger.New("error"), chat.WithClock(w.clock), chat.WithAutoMod(terms{"spam"}))
}

// guestStep is one send; p zero = guest A; name "" = "Mèo Con".
type guestStep struct {
	p       entity.Principal
	name    string
	body    string
	wait    time.Duration
	wantErr error
	details map[string]string
}

func TestSendGuestRules(t *testing.T) { //nolint:funlen,maintidx // one table of every guest rule
	t.Parallel()

	ten := 10
	nameDetail := func(d string) map[string]string { return map[string]string{"guestName": d} }

	tests := []struct {
		name  string
		cfg   func(*chat.Config)
		setup func(*world)
		steps []guestStep
	}{
		{
			name: "guest chat off: unauthorized, even with a bad nickname", setup: func(w *world) { w.settings.GuestChat = false },
			steps: []guestStep{
				{body: "hi", wantErr: entity.ErrUnauthenticated},
				{name: "x", body: "hi", wantErr: entity.ErrUnauthenticated},
			},
		},
		{
			name:  "no guest key (no valid anonId)",
			steps: []guestStep{{p: entity.Principal{IP: guestIP}, body: "hi", wantErr: entity.ErrUnauthenticated}},
		},
		{
			name: "verified e-mail required", cfg: func(c *chat.Config) { c.RequireVerified = true },
			steps: []guestStep{{body: "hi", wantErr: entity.ErrUnauthenticated}},
		},
		{name: "accepted", steps: []guestStep{{body: "xin chào"}}},
		{name: "nickname missing", steps: []guestStep{{name: " ", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("REQUIRED")}}},
		{name: "nickname too short", steps: []guestStep{{name: "ab", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("LENGTH")}}},
		{
			name:  "nickname too long",
			steps: []guestStep{{name: strings.Repeat("a", 21), body: "hi", wantErr: entity.ErrValidation, details: nameDetail("LENGTH")}},
		},
		{name: "nickname format", steps: []guestStep{{name: "<b>x</b>", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("FORMAT")}}},
		{name: "nickname link", steps: []guestStep{{name: "vao abc.vn", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("BLOCKED")}}},
		{
			name:  "nickname with a blocked term of the channel",
			steps: []guestStep{{name: "Spam King", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("BLOCKED")}},
		},
		{name: "reserved nickname", steps: []guestStep{{name: "Quản trị viên", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("TAKEN")}}},
		{
			name:  "nickname is an account's username",
			setup: func(w *world) { w.users[viewer] = entity.User{ID: viewer, Username: "viewer_a"} },
			steps: []guestStep{{name: "Viewer_A", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("TAKEN")}},
		},
		{
			name:  "nickname is the channel's handle or name",
			setup: func(w *world) { w.ownerID, w.handle, w.chName = owner, "kaelen", "Kaelen Gaming" },
			steps: []guestStep{
				{name: "KAELEN", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("TAKEN")},
				{name: "kaelen gaming", body: "hi", wantErr: entity.ErrValidation, details: nameDetail("TAKEN")},
				{name: "Kaelen fan", body: "hi"},
			},
		},
		{
			name:  "guest message longer than CHAT_GUEST_MAX_CHARS",
			steps: []guestStep{{body: strings.Repeat("a", 201), wantErr: entity.ErrValidation, details: map[string]string{"body": "LENGTH"}}},
		},
		{
			name: "links always blocked for guests",
			steps: []guestStep{
				{body: "xem abc.xyz", wantErr: entity.ErrContentBlocked, details: map[string]string{"reason": "LINK"}},
			},
		},
		{
			// The fake bucket never refills: a refused message keeps its tokens.
			name: "guest slow: 10 s between two messages", cfg: func(c *chat.Config) { c.GuestRateLimitIP = 10 },
			steps: []guestStep{
				{body: "one"},
				{body: "two", wait: 3 * time.Second, wantErr: entity.ErrSlowMode, details: map[string]string{"retryAfterSec": "7"}},
				{body: "three", wait: 7 * time.Second},
			},
		},
		{
			name: "channel slow mode longer than the guest one", setup: func(w *world) { w.settings.SlowSec = 30 },
			steps: []guestStep{
				{body: "one"},
				{body: "two", wait: 10 * time.Second, wantErr: entity.ErrSlowMode, details: map[string]string{"retryAfterSec": "20"}},
			},
		},
		{
			name: "every guest of one IP shares a bucket; accounts on that IP do not",
			steps: []guestStep{
				{p: guest(anonA), body: "a"},
				{p: guest(anonB), body: "b"},
				{p: guest(anonC), body: "c", wantErr: entity.ErrRateLimited, details: map[string]string{"retryAfterSec": "2"}},
				{p: entity.Principal{UserID: viewer, IP: guestIP}, body: "user"},
			},
		},
		{
			name: "followers only blocks guests", setup: func(w *world) { w.settings.FollowersMin = &ten },
			steps: []guestStep{{body: "hi", wantErr: entity.ErrFollowersOnly, details: map[string]string{"requiredMin": "10"}}},
		},
		{
			name: "subs only blocks guests", setup: func(w *world) { w.settings.SubsOnly = true },
			steps: []guestStep{{body: "hi", wantErr: entity.ErrSubsOnly}},
		},
		{
			name: "timed out by guest key", setup: func(w *world) { w.timeouts[guestKey(anonA)] = w.now.Add(time.Minute) },
			steps: []guestStep{
				{body: "hi", wantErr: entity.ErrTimedOut, details: map[string]string{"until": "2026-10-14T12:01:00Z"}},
				{p: guest(anonB), body: "hi"},
			},
		},
		{
			name: "banned by guest key", setup: func(w *world) { w.banned[guestKey(anonA)] = true },
			steps: []guestStep{{body: "hi", wantErr: entity.ErrBanned}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.settings.GuestChat = true

			if tc.setup != nil {
				tc.setup(w)
			}

			cfg := defaultConfig()
			if tc.cfg != nil {
				tc.cfg(&cfg)
			}

			refused := runGuestSteps(t, newGuestUC(w, cfg), w, tc.steps)
			require.Len(t, w.inserted, len(tc.steps)-refused)
		})
	}
}

// runGuestSteps sends each step and checks its outcome; it returns how many were refused.
func runGuestSteps(t *testing.T, uc *chat.UseCase, w *world, steps []guestStep) int {
	t.Helper()

	refused := 0

	for i, s := range steps {
		w.advance(s.wait)

		p, name := s.p, s.name
		if p == (entity.Principal{}) {
			p = guest(anonA)
		}

		if name == "" {
			name = "Mèo Con"
		}

		in := msg(s.body)
		in.GuestName = name

		_, err := uc.Send(context.Background(), p, streamID, in)
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

func TestSendGuestMessage(t *testing.T) {
	t.Parallel()

	w := newWorld()
	w.settings.GuestChat = true
	w.ownerID = owner
	w.users[viewer] = entity.User{ID: viewer, Username: "viewer_a"}
	uc := newGuestUC(w, defaultConfig())
	ctx := context.Background()
	key := guestKey(anonA)

	in := msg("chào @viewer_a nxGG")
	in.GuestName = "  Mèo   Con "

	res, err := uc.Send(ctx, guest(anonA), streamID, in)
	require.NoError(t, err)

	want := entity.Author{ID: key, DisplayName: "Mèo Con", Color: entity.ColorOf(key), Badges: []string{"GUEST"}}
	require.Equal(t, want, res.Message.User)
	require.Empty(t, res.Message.Mentioned(), "guests mention nobody")
	require.Equal(t, "emote", res.Message.Fragments[1].Type, "global emotes still work")
	require.Equal(t, 1, w.buckets["chat:rate:"+key])
	require.Equal(t, 1, w.buckets["chat:rate:gip:"+guestIP])

	again, err := uc.Send(ctx, guest(anonA), streamID, in)
	require.NoError(t, err)
	require.True(t, again.Duplicate, "retries dedupe on the guest key")

	page, _, err := uc.History(ctx, streamID, nil, 10)
	require.NoError(t, err)
	require.Equal(t, want, page[0].User, "history keeps the nickname and the GUEST badge")
}

func TestSnapshotGuest(t *testing.T) {
	t.Parallel()

	ten := 10
	ctx := context.Background()

	off := newWorld()
	snap, err := newGuestUC(off, defaultConfig()).Snapshot(ctx, guest(anonA), streamID)
	require.NoError(t, err)
	require.Equal(t, "GUEST", snap.Me.Role)
	require.Equal(t, []string{}, snap.Me.Badges)
	require.False(t, snap.Me.CanSend)
	require.Equal(t, "UNAUTHORIZED", *snap.Me.Reason)
	require.False(t, snap.Room.Mode.GuestChat)

	on := newWorld()
	on.settings.GuestChat = true
	uc := newGuestUC(on, defaultConfig())

	snap, err = uc.Snapshot(ctx, guest(anonA), streamID)
	require.NoError(t, err)
	require.True(t, snap.Room.Mode.GuestChat)
	require.True(t, snap.Me.CanSend)
	require.Nil(t, snap.Me.Reason)
	require.Equal(t, []string{"GUEST"}, snap.Me.Badges)
	require.Equal(t, entity.ColorOf(guestKey(anonA)), snap.Me.Color)

	in := msg("hi")
	in.GuestName = "Mèo Con"
	_, err = uc.Send(ctx, guest(anonA), streamID, in)
	require.NoError(t, err)

	snap, err = uc.Snapshot(ctx, guest(anonA), streamID)
	require.NoError(t, err)
	require.False(t, snap.Me.CanSend)
	require.Equal(t, "CHAT_SLOW_MODE", *snap.Me.Reason)
	require.Equal(t, 10, *snap.Me.RetryAfterSec)

	noKey, err := uc.Snapshot(ctx, entity.Principal{ConnID: "c"}, streamID)
	require.NoError(t, err)
	require.Equal(t, "UNAUTHORIZED", *noKey.Me.Reason)

	on.settings.FollowersMin = &ten
	snap, err = uc.Snapshot(ctx, guest(anonB), streamID)
	require.NoError(t, err)
	require.False(t, snap.Me.CanSend)
	require.Equal(t, "CHAT_FOLLOWERS_ONLY", *snap.Me.Reason)
}

func TestUpdateSettingsGuestChat(t *testing.T) {
	t.Parallel()

	mod := "0199d4a2-0000-7000-8000-000000000004"
	admin := entity.Principal{UserID: other, GlobalRole: entity.GlobalRoleAdmin}

	tests := []struct {
		name    string
		p       entity.Principal
		was     bool
		in      entity.ChatSettings
		wantErr error
	}{
		{name: "owner turns it on", p: user(owner), in: entity.ChatSettings{GuestChat: true}},
		{name: "admin turns it off", p: admin, was: true, in: entity.ChatSettings{}},
		{name: "moderator turns it on", p: user(mod), in: entity.ChatSettings{GuestChat: true}, wantErr: entity.ErrForbidden},
		{name: "moderator turns it off", p: user(mod), was: true, in: entity.ChatSettings{}, wantErr: entity.ErrForbidden},
		{name: "moderator changes another mode, guest chat kept", p: user(mod), was: true, in: entity.ChatSettings{SlowSec: 5, GuestChat: true}},
		{name: "viewer", p: user(viewer), in: entity.ChatSettings{GuestChat: true}, wantErr: entity.ErrForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.ownerID = owner
			w.roles[mod] = entity.ChannelRoleModerator
			w.openRooms = []string{streamID}
			w.settings.GuestChat = tc.was
			uc, _ := newUC(w, defaultConfig())

			tc.in.ChannelID = channelID

			got, err := uc.UpdateSettings(context.Background(), tc.p, tc.in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, tc.was, w.settings.GuestChat, "unchanged")
				require.Empty(t, w.published)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.in.GuestChat, got.GuestChat)
			require.Len(t, w.settingsEvents, 1)
			require.Equal(t, tc.was, w.settingsEvents[0].Before.GuestChat)
			require.Equal(t, tc.in.GuestChat, w.settingsEvents[0].After.GuestChat)
			require.Equal(t, entity.EventModeChange, w.published[0].typ)

			mode, ok := w.published[0].data.(entity.Mode)
			require.True(t, ok)
			require.Equal(t, tc.in.GuestChat, mode.GuestChat)
		})
	}
}
