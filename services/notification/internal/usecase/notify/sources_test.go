package notify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
	"github.com/stretchr/testify/require"
)

func started() events.StreamStarted {
	return events.StreamStarted{StreamID: streamID, ChannelID: channelID, Title: "Đêm nhạc"}
}

func TestCreatorLive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		verified  bool
		override  *entity.Setting
		inAppCool string
		emailCool string
		today     int
		wantApp   bool
		wantMail  bool
		reason    string
	}{
		{name: "in-app and e-mail", verified: true, wantApp: true, wantMail: true},
		{name: "unverified: in-app only", verified: false, wantApp: true, reason: usecase.SuppressNoEmail},
		{
			name: "e-mail turned off", verified: true, wantApp: true,
			override: &entity.Setting{Type: entity.TypeCreatorLive, InApp: true},
		},
		{
			name: "in-app turned off: e-mail only", verified: true, wantMail: true,
			override: &entity.Setting{Type: entity.TypeCreatorLive, Email: true},
		},
		{
			name: "both off", verified: true, reason: usecase.SuppressSettings,
			override: &entity.Setting{Type: entity.TypeCreatorLive},
		},
		{
			name: "re-live within 30 min: nothing", verified: true, inAppCool: otherEvent, emailCool: otherEvent,
			reason: usecase.SuppressCooldown,
		},
		{name: "after 30 min, within 12 h: in-app only", verified: true, emailCool: otherEvent, wantApp: true},
		{
			name: "redelivery of the same event is not cooled down", verified: true, inAppCool: eventID, emailCool: eventID,
			wantApp: true, wantMail: true,
		},
		{name: "daily cap reached", verified: true, today: 10, wantApp: true, reason: usecase.SuppressDailyCap},
		{name: "below the cap", verified: true, today: 9, wantApp: true, wantMail: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := liveEnv(tc.verified, tc.override, tc.inAppCool, tc.emailCool, tc.today)

			require.NoError(t, e.uc.CreatorLive(context.Background(), eventID, started()))
			require.Len(t, e.store.batches, 1)

			b := e.store.batches[0]
			require.Equal(t, tc.wantApp, len(b.users) == 1, "in-app")
			require.Equal(t, tc.wantMail, len(b.emails) == 1, "e-mail")

			if tc.reason != "" {
				require.Positive(t, e.obs.suppressed[tc.reason], tc.reason)
			}

			if tc.wantApp {
				require.Equal(t, eventID, e.throttle.inApp[viewerID], "cooldown set to this event")
			}
		})
	}
}

// liveEnv has one follower with the given settings override, cooldown holders and e-mails sent today.
func liveEnv(verified bool, override *entity.Setting, inAppCool, emailCool string, today int) *env {
	e := newEnv()
	e.store.followers = []entity.Recipient{recipient(viewerID, verified)}

	if override != nil {
		e.store.overrides[viewerID] = map[entity.Type]entity.Setting{entity.TypeCreatorLive: *override}
	}

	if inAppCool != "" {
		e.throttle.inApp[viewerID] = inAppCool
	}

	if emailCool != "" {
		e.throttle.email[viewerID] = emailCool
	}

	e.throttle.today[viewerID] = today

	return e
}

func TestCreatorLivePayloadAndEmail(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.followers = []entity.Recipient{recipient(viewerID, true)}

	require.NoError(t, e.uc.CreatorLive(context.Background(), eventID, started()))

	// BR-E04: followers with the bell on ALL or LIVE_ONLY.
	require.Equal(t, [][]string{{events.NotifyAll, events.NotifyLiveOnly}}, e.store.levels)

	b := e.store.batches[0]
	require.Equal(t, entity.TypeCreatorLive, b.n.Type)
	require.Equal(t, "/live/"+streamID, *b.n.Link)

	var p map[string]any
	require.NoError(t, json.Unmarshal(b.n.Payload, &p))
	require.Equal(t, "Đêm nhạc", p["title"])
	ch, ok := p["channel"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "kenh", ch["handle"])

	m := b.emails[0]
	require.Equal(t, "live_started", m.Template)
	require.False(t, m.Mandatory)
	require.Equal(t, "http://web.test/live/"+streamID, m.Data["Link"])
	require.True(t, strings.HasPrefix(m.Data["UnsubscribeURL"], "http://web.test/notifications/unsubscribe?token="))
	require.Equal(t, map[string]string{viewerID: "20261016"}, e.throttle.counted, "day in Asia/Ho_Chi_Minh")
}

func TestCreatorLiveBatchesOf500(t *testing.T) {
	t.Parallel()

	e := newEnv()
	for i := range 1001 {
		e.store.followers = append(e.store.followers, recipient(fmtID(i), false))
	}

	require.NoError(t, e.uc.CreatorLive(context.Background(), eventID, started()))
	require.Len(t, e.store.batches, 3)
	require.Len(t, e.store.batches[0].users, 500)
	require.Len(t, e.store.batches[2].users, 1)

	// A redelivery inserts nothing new (unique (user_id, source_event_id) in the fake too).
	before := len(e.store.notifyRows)
	require.NoError(t, e.uc.CreatorLive(context.Background(), eventID, started()))
	require.Len(t, e.store.notifyRows, before)
}

func fmtID(i int) string { return fmt.Sprintf("0199d4a2-0000-7000-9000-%012d", i) }

func TestCreatorLiveMissingChannel(t *testing.T) {
	t.Parallel()

	e := newEnv()
	ev := started()
	ev.ChannelID = streamID

	require.ErrorIs(t, e.uc.CreatorLive(context.Background(), eventID, ev), entity.ErrNotFound)
}

func TestScheduleCreatedIsInAppForAllOnly(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.followers = []entity.Recipient{recipient(viewerID, true)}

	require.NoError(t, e.uc.ScheduleCreated(context.Background(), eventID, events.ScheduleCreated{
		ScheduleID: eventID, ChannelID: channelID, Title: "Lịch", StartAt: now,
	}))
	require.Equal(t, [][]string{{events.NotifyAll}}, e.store.levels)
	require.Len(t, e.store.batches[0].users, 1)
	require.Empty(t, e.store.batches[0].emails, "SCHEDULE CREATED has no e-mail")
	require.Contains(t, string(e.store.batches[0].n.Payload), `"reason":"CREATED"`)
}

func TestStreamStartingSkipsMissingAndLocked(t *testing.T) {
	t.Parallel()

	e := newEnv()
	locked := recipient(viewer2ID, true)
	locked.Active = false
	e.store.addUsers(recipient(viewerID, true), locked)

	require.NoError(t, e.uc.StreamStarting(context.Background(), eventID, events.ScheduleReminderDue{
		ScheduleID: eventID, ChannelID: channelID, Title: "T", OccurrenceAt: now,
		UserIDs: []string{viewerID, viewer2ID, senderID, viewerID},
	}))
	require.Equal(t, []string{viewerID}, e.store.batches[0].users)
	require.Equal(t, "/kenh?tab=schedule", *e.store.batches[0].n.Link)
	require.Equal(t, "03:30 16/10/2026 (+07)", e.store.batches[0].emails[0].Data["When"])
	require.Equal(t, 1, e.obs.suppressed[usecase.SuppressNoCopy])
	require.Equal(t, 1, e.obs.suppressed[usecase.SuppressInactive])
}

func TestMention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		blocked bool
		email   bool
		want    int
		mail    bool
	}{
		{name: "grouped per stream, in-app only by default", want: 1},
		{name: "blocked sender: nothing", blocked: true},
		{name: "e-mail turned on", email: true, want: 1, mail: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()
			e.store.addUsers(recipient(viewerID, true), recipient(senderID, true))
			e.store.blocked[viewerID] = tc.blocked

			if tc.email {
				e.store.overrides[viewerID] = map[entity.Type]entity.Setting{entity.TypeMention: {
					Type: entity.TypeMention, InApp: true, Email: true,
				}}
			}

			err := e.uc.Mention(context.Background(), eventID, events.ChatMention{
				MessageID: eventID, StreamID: streamID, ChannelID: channelID, SenderID: senderID, SenderUsername: "b",
				MentionedUserIDs: []string{senderID, viewerID, viewerID}, Excerpt: "@an hi",
			})
			require.NoError(t, err)

			if tc.want == 0 {
				require.Empty(t, e.store.upserts)
				require.Equal(t, 1, e.obs.suppressed[usecase.SuppressBlocked])

				return
			}

			items := e.store.upserts[0]
			require.Len(t, items, tc.want)
			require.Equal(t, "MENTION:"+streamID, *items[0].Notification.GroupKey)
			require.Equal(t, 5*time.Minute, items[0].Window)
			require.Equal(t, tc.mail, items[0].Email != nil)
			require.Contains(t, string(items[0].Notification.Payload), `"lastSender":{"id":"`+senderID)
		})
	}
}

func TestNewFollowerGroupsForTheOwner(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(ownerID, true), recipient(viewerID, true))

	require.NoError(t, e.uc.NewFollower(context.Background(), eventID, events.FollowCreated{UserID: viewerID, ChannelID: channelID}))

	it := e.store.upserts[0][0]
	require.Equal(t, ownerID, it.Notification.UserID)
	require.Equal(t, "NEW_FOLLOWER:"+channelID, *it.Notification.GroupKey)
	require.Equal(t, time.Hour, it.Window)
	require.Nil(t, it.Email, "NEW_FOLLOWER has no e-mail")

	// The same event again is a no-op (processed_events).
	require.NoError(t, e.uc.NewFollower(context.Background(), eventID, events.FollowCreated{UserID: viewerID, ChannelID: channelID}))
	require.Len(t, e.store.upserts, 1)
}

func TestMandatoryEmails(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(ownerID, true))
	e.throttle.today[ownerID] = 50

	// Locked types ignore a stored override and the daily cap.
	e.store.overrides[ownerID] = map[entity.Type]entity.Setting{entity.TypeSystem: {Type: entity.TypeSystem}}

	require.NoError(t, e.uc.StreamTakedown(context.Background(), eventID, events.StreamTakedown{
		ActionID: eventID, StreamID: streamID, ChannelID: channelID, Title: "T", Reason: "violent content",
	}))

	it := e.store.upserts[0][0]
	require.Equal(t, entity.TypeSystem, it.Notification.Type)
	require.Contains(t, string(it.Notification.Payload), `"kind":"STREAM_TAKEDOWN"`)
	require.NotNil(t, it.Email)
	require.True(t, it.Email.Mandatory)
	require.Equal(t, "stream_taken_down", it.Email.Template)
	require.NotContains(t, it.Email.Data, "UnsubscribeURL")
	require.Empty(t, e.throttle.counted, "mandatory e-mails are not counted")
}

func TestCreatorResult(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(viewerID, true))

	reason := "thiếu thông tin"

	require.NoError(t, e.uc.CreatorResult(context.Background(), eventID, false, eventID, viewerID, &reason))

	it := e.store.upserts[0][0]
	require.Equal(t, "/creator/apply", *it.Notification.Link)
	require.Contains(t, string(it.Notification.Payload), `"result":"REJECTED"`)
	require.Equal(t, "creator_application_result", it.Email.Template)
}

func TestReportResultDefaultsToInApp(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(viewerID, true))

	require.NoError(t, e.uc.ReportResult(context.Background(), eventID, events.ReportResolved{
		ReportID: eventID, ReporterID: viewerID, TargetType: "MESSAGE", Status: events.ReportStatusResolved,
	}))

	it := e.store.upserts[0][0]
	require.Nil(t, it.Notification.Link)
	require.Nil(t, it.Email, "report_result e-mail is off by default")
}

func TestAccountLockedEvenUnverified(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(viewerID, false))

	require.NoError(t, e.uc.AccountLocked(context.Background(), eventID, events.UserLocked{UserID: viewerID, Reason: "spam"}))
	require.Len(t, e.store.queued, 1)
	require.Equal(t, "account_locked", e.store.queued[0].Template)
	require.True(t, e.store.queued[0].Mandatory)
	require.Empty(t, e.store.queued[0].Data["Until"], "permanent")
}
