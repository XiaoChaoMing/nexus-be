package persistent_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

func outboxTypes(t *testing.T, pg *postgres.Postgres) []string {
	t.Helper()

	rows, err := pg.Pool.Query(context.Background(), `SELECT event_type FROM outbox ORDER BY created_at, id`)
	require.NoError(t, err)

	defer rows.Close()

	var out []string

	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))

		out = append(out, s)
	}

	require.NoError(t, rows.Err())

	return out
}

func lastPayload[T any](t *testing.T, pg *postgres.Postgres, eventType string) T {
	t.Helper()

	var (
		b   []byte
		out T
	)

	require.NoError(t, pg.Pool.QueryRow(context.Background(),
		`SELECT payload FROM outbox WHERE event_type = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, eventType).Scan(&b))
	require.NoError(t, json.Unmarshal(b, &out))

	return out
}

func newChannel(t *testing.T, r *persistent.Repo, handle string) entity.Channel {
	t.Helper()

	ch, created, err := r.CreateForOwner(context.Background(), "", repo.NewChannel{OwnerID: uuid.NewString(), BaseHandle: handle, DisplayName: handle})
	require.NoError(t, err)
	require.True(t, created)

	return ch
}

func TestFollows(t *testing.T) { //nolint:paralleltest,funlen // shared database; one lifecycle
	r, pg := setup(t)
	ctx := context.Background()
	a, b := newChannel(t, r, "alpha"), newChannel(t, r, "bravo")
	user := uuid.NewString()

	f, created, err := r.Follow(ctx, user, a.ID)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, entity.NotifyAll, f.NotifyLevel)

	_, created, err = r.Follow(ctx, user, a.ID)
	require.NoError(t, err)
	require.False(t, created, "following twice is a no-op")

	_, _, err = r.Follow(ctx, user, b.ID)
	require.NoError(t, err)

	got, err := r.ByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.FollowerCount)

	_, err = r.SetNotifyLevel(ctx, user, a.ID, entity.NotifyLiveOnly)
	require.NoError(t, err)
	_, err = r.SetNotifyLevel(ctx, user, a.ID, entity.NotifyLiveOnly)
	require.NoError(t, err, "same level: no second event")
	_, err = r.SetNotifyLevel(ctx, uuid.NewString(), a.ID, entity.NotifyOff)
	require.ErrorIs(t, err, entity.ErrNotFollowing)

	created1 := lastPayload[events.FollowCreated](t, pg, events.TypeFollowCreated)
	require.Equal(t, "ALL", created1.NotifyLevel)
	require.Equal(t, "LIVE_ONLY", lastPayload[events.FollowUpdated](t, pg, events.TypeFollowUpdated).NotifyLevel)

	// b goes live and was live more recently than a.
	require.NoError(t, r.SetLive(ctx, "", entity.LiveStream{StreamID: uuid.NewString(), ChannelID: b.ID, Title: "t", StartedAt: time.Now()}))

	page1, err := r.ListFollowed(ctx, user, false, nil, 1)
	require.NoError(t, err)
	require.Len(t, page1, 1)
	require.Equal(t, b.ID, page1[0].Channel.ID, "live first")

	cur := &repo.FollowCursor{IsLive: true, LastLiveAt: time.Unix(0, 0), ChannelID: b.ID}
	page2, err := r.ListFollowed(ctx, user, false, cur, 10)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, a.ID, page2[0].Channel.ID)
	require.Equal(t, entity.NotifyLiveOnly, page2[0].NotifyLevel)

	live, err := r.ListLive(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, live, 1)

	n, err := r.CountByUser(ctx, user)
	require.NoError(t, err)
	require.Equal(t, 2, n)

	require.NoError(t, r.Unfollow(ctx, user, a.ID))
	require.NoError(t, r.Unfollow(ctx, user, a.ID))

	got, err = r.ByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 0, got.FollowerCount)

	require.Equal(t, []string{
		events.TypeChannelCreated, events.TypeChannelCreated, events.TypeFollowCreated, events.TypeFollowCreated,
		events.TypeFollowUpdated, events.TypeFollowDeleted,
	}, outboxTypes(t, pg))
}

func TestUpdateProfile(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	a, b := newChannel(t, r, "alpha"), newChannel(t, r, "bravo")
	now := time.Now().UTC().Truncate(time.Second)
	bio, name := "Xin chào", "Alpha Kênh"
	links := []entity.SocialLink{{Type: "YOUTUBE", URL: "https://youtube.com/@a"}}

	got, err := r.UpdateProfile(ctx, a.ID, entity.ProfilePatch{Bio: &bio, SocialLinks: &links}, now)
	require.NoError(t, err)
	require.Equal(t, bio, *got.Bio)
	require.Equal(t, links, got.SocialLinks)
	require.Equal(t, []string{events.TypeChannelCreated, events.TypeChannelCreated}, outboxTypes(t, pg),
		"bio and links do not announce channel.updated")

	got, err = r.UpdateProfile(ctx, a.ID, entity.ProfilePatch{ClearBio: true, DisplayName: &name}, now)
	require.NoError(t, err)
	require.Nil(t, got.Bio)
	require.Equal(t, name, lastPayload[events.ChannelUpdated](t, pg, events.TypeChannelUpdated).DisplayName)

	taken := "BRAVO"
	_, err = r.UpdateProfile(ctx, a.ID, entity.ProfilePatch{Handle: &taken}, now)
	require.ErrorIs(t, err, entity.ErrHandleTaken, "citext uniqueness")

	h := "alpha_new"
	got, err = r.UpdateProfile(ctx, a.ID, entity.ProfilePatch{Handle: &h}, now)
	require.NoError(t, err)
	require.Equal(t, h, got.Handle)
	require.True(t, got.HandleChangedAt.Equal(now))

	_, err = r.ByHandle(ctx, "alpha")
	require.ErrorIs(t, err, entity.ErrNotFound, "old handle no longer resolves")

	docs, err := r.Search(ctx, "alpha", entity.DocChannel, nil, 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "/alpha_new", docs[0].Target)

	old, err := r.SetImage(ctx, b.ID, entity.ImageAvatar, "/media/channels/b/avatar-1.jpg")
	require.NoError(t, err)
	require.Nil(t, old)

	old, err = r.SetImage(ctx, b.ID, entity.ImageAvatar, "/media/channels/b/avatar-2.jpg")
	require.NoError(t, err)
	require.Equal(t, "/media/channels/b/avatar-1.jpg", *old)
	require.Equal(t, "/media/channels/b/avatar-2.jpg", *lastPayload[events.ChannelUpdated](t, pg, events.TypeChannelUpdated).AvatarURL)
}

func TestSearch(t *testing.T) { //nolint:paralleltest,funlen // shared database; one table of queries
	r, _ := setup(t)
	ctx := context.Background()
	ch := newChannel(t, r, "vozer")

	cats, err := r.Categories(ctx, nil)
	require.NoError(t, err)
	require.Len(t, cats, 24)

	lmht, err := r.BySlug(ctx, "lien-minh-huyen-thoai")
	require.NoError(t, err)
	require.NoError(t, r.SetLive(ctx, "", entity.LiveStream{
		StreamID: uuid.NewString(), ChannelID: ch.ID, Title: "Đường giữa Yasuo",
		CategoryID: &lmht.ID, StartedAt: time.Now(),
	}))

	for _, q := range []string{"lien minh", "Liên Minh", "LIÊN MINH", "Liên minh", "lien minh huyen"} {
		docs, serr := r.Suggest(ctx, q, entity.DocTypes(), 5)
		require.NoError(t, serr, q)

		var titles []string
		for _, d := range docs {
			titles = append(titles, d.Title)
		}

		require.Contains(t, titles, "Liên Minh Huyền Thoại", q)
	}

	docs, err := r.Suggest(ctx, "duong giua", entity.DocTypes(), 5)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, entity.DocStream, docs[0].Type)

	docs, err = r.Suggest(ctx, "val", []entity.DocType{entity.DocCategory}, 5)
	require.NoError(t, err)
	require.Equal(t, "Valorant", docs[0].Title, "prefix hit first")

	for _, hostile := range []string{"%_", `\\`, "a & b | !c", "':*", "lien:* & minh"} {
		_, err = r.Suggest(ctx, hostile, entity.DocTypes(), 5)
		require.NoError(t, err, hostile)
	}

	docs, err = r.Suggest(ctx, "%%", entity.DocTypes(), 5)
	require.NoError(t, err)
	require.Empty(t, docs, "LIKE wildcards are literal")

	// Keyset paging returns every esports category exactly once.
	seen := map[string]bool{}

	var after *repo.SearchCursor

	for range 10 {
		page, perr := r.Search(ctx, "esports", entity.DocCategory, after, 2)
		require.NoError(t, perr)

		if len(page) == 0 {
			break
		}

		for _, d := range page {
			require.False(t, seen[d.ID])
			seen[d.ID] = true
		}

		last := page[len(page)-1]
		after = &repo.SearchCursor{PrefixHit: last.PrefixHit, IsLive: last.IsLive, Rank: last.Rank, Viewers: last.Viewers, ID: last.ID}
	}

	require.Len(t, seen, 4)

	vod := uuid.NewString()
	require.NoError(t, r.UpsertVOD(ctx, "", entity.VODDoc{
		VODID: vod, ChannelID: ch.ID, Title: "Highlight cũ", Public: true,
		ExpiresAt: time.Now().Add(-time.Hour),
	}))

	docs, err = r.Suggest(ctx, "highlight", entity.DocTypes(), 5)
	require.NoError(t, err)
	require.Empty(t, docs, "expired VODs are not found")
}

func TestSearchHistory(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	user := uuid.NewString()

	first, err := r.Remember(ctx, user, "Valorant", 3)
	require.NoError(t, err)

	for _, q := range []string{"a1", "a2", "a3"} {
		_, err = r.Remember(ctx, user, q, 3)
		require.NoError(t, err)
	}

	again, err := r.Remember(ctx, user, "valorant", 3)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, again.ID, "the old entry was trimmed")

	list, err := r.History(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, list, 3)
	require.Equal(t, "valorant", list[0].Query)

	same, err := r.Remember(ctx, user, "A3", 3)
	require.NoError(t, err)
	require.Equal(t, list[1].ID, same.ID, "case-insensitive duplicate keeps its id")

	require.NoError(t, r.Forget(ctx, user, &same.ID))
	require.NoError(t, r.Forget(ctx, uuid.NewString(), nil))

	list, err = r.History(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
}

func weekly(t *testing.T, ch entity.Channel, start time.Time, rule string) entity.Schedule {
	t.Helper()

	in := entity.ScheduleInput{Title: "Rank tối", StartAt: start, DurationMin: 60, RRule: rule, Timezone: "Asia/Ho_Chi_Minh"}
	s, errs := in.Build(entity.ScheduleRules{Now: start.Add(-time.Hour), Horizon: 90 * 24 * time.Hour})
	require.Empty(t, errs)

	s.ChannelID = ch.ID

	return s
}

func TestSchedules(t *testing.T) { //nolint:paralleltest,funlen // shared database; one lifecycle
	r, pg := setup(t)
	ctx := context.Background()
	ch := newChannel(t, r, "sched")
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(2 * time.Hour)

	s, err := r.Create(ctx, weekly(t, ch, start, "FREQ=WEEKLY;BYDAY="+dayCode(start)), 2, now)
	require.NoError(t, err)
	require.NotNil(t, s.RRule)

	_, err = r.Create(ctx, weekly(t, ch, start.Add(time.Hour), ""), 2, now)
	require.NoError(t, err)

	_, err = r.Create(ctx, weekly(t, ch, start.Add(2*time.Hour), ""), 2, now)
	require.ErrorIs(t, err, entity.ErrScheduleLimit)

	// Two job instances at once: SKIP LOCKED gives each schedule to one of them.
	var (
		wg     sync.WaitGroup
		counts [2]int
		errs   [2]error
	)

	for i := range counts {
		wg.Go(func() {
			counts[i], errs[i] = r.AnnounceDue(ctx, now.Add(24*time.Hour), 10, func(sc entity.Schedule) []events.ScheduleOccurrenceUpcoming {
				time.Sleep(50 * time.Millisecond)

				return []events.ScheduleOccurrenceUpcoming{{ScheduleID: sc.ID, ChannelID: sc.ChannelID, OccurrenceAt: sc.StartAt}}
			})
		})
	}

	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Equal(t, 2, counts[0]+counts[1])

	n, err := r.AnnounceDue(ctx, now.Add(24*time.Hour), 10, func(entity.Schedule) []events.ScheduleOccurrenceUpcoming {
		t.Fatal("watermark reached: nothing is due")

		return nil
	})
	require.NoError(t, err)
	require.Zero(t, n)

	stream := uuid.NewString()
	require.NoError(t, r.UpsertOccurrenceStream(ctx, uuid.NewString(), events.StreamScheduled{
		StreamID: stream, ScheduleID: s.ID,
		ScheduledStartAt: start,
	}))

	viewer := uuid.NewString()
	_, created, err := r.Add(ctx, entity.Reminder{UserID: viewer, ScheduleID: s.ID, OccurrenceAt: start})
	require.NoError(t, err)
	require.True(t, created)
	_, created, err = r.Add(ctx, entity.Reminder{UserID: viewer, ScheduleID: s.ID, OccurrenceAt: start})
	require.NoError(t, err)
	require.False(t, created)

	counted, err := r.Counts(ctx, []string{s.ID}, now)
	require.NoError(t, err)
	require.Equal(t, 1, counted[entity.KeyOf(s.ID, start)])

	moved := weekly(t, ch, start.Add(30*time.Minute), "")
	moved.ParentID = &s.ID

	m, err := r.MoveOccurrence(ctx, s, start, moved)
	require.NoError(t, err)

	ev := lastPayload[events.ScheduleOccurrenceChanged](t, pg, events.TypeScheduleOccurrenceChanged)
	require.Equal(t, events.OccurrenceMoved, ev.Reason)
	require.Equal(t, m.ID, *ev.NewScheduleID)
	require.Equal(t, []string{viewer}, ev.UserIDs, "the reminder follows the moved occurrence")

	streams, err := r.OccurrenceStreams(ctx, []string{m.ID})
	require.NoError(t, err)
	require.Equal(t, stream, streams[entity.KeyOf(m.ID, m.StartAt)])

	err = r.CancelOccurrence(ctx, s, start)
	require.ErrorIs(t, err, entity.ErrInvalidState, "already excluded")

	got, err := r.Get(ctx, ch.ID, s.ID)
	require.NoError(t, err)
	require.False(t, got.HasOccurrence(start))

	require.NoError(t, r.Cancel(ctx, m, now))
	require.ErrorIs(t, r.Cancel(ctx, m, now), entity.ErrInvalidState)

	ev = lastPayload[events.ScheduleOccurrenceChanged](t, pg, events.TypeScheduleOccurrenceChanged)
	require.Equal(t, events.OccurrenceCancelled, ev.Reason)
	require.Equal(t, []string{viewer}, ev.UserIDs)

	_, err = r.Get(ctx, uuid.NewString(), s.ID)
	require.ErrorIs(t, err, entity.ErrNotFound, "another channel's schedule")
}

func dayCode(t time.Time) string {
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh") //nolint:errcheck // embedded zone database

	return []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[t.In(loc).Weekday()]
}

func TestMarkDue(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ch := newChannel(t, r, "remind")
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(5 * time.Minute)

	s, err := r.Create(ctx, weekly(t, ch, at, ""), 20, now)
	require.NoError(t, err)

	for range 3 {
		_, _, err = r.Add(ctx, entity.Reminder{UserID: uuid.NewString(), ScheduleID: s.ID, OccurrenceAt: at})
		require.NoError(t, err)
	}

	n, err := r.MarkDue(ctx, time.Minute, 100)
	require.NoError(t, err)
	require.Zero(t, n, "not due yet")

	n, err = r.MarkDue(ctx, 10*time.Minute, 100)
	require.NoError(t, err)
	require.Equal(t, 3, n)

	n, err = r.MarkDue(ctx, 10*time.Minute, 100)
	require.NoError(t, err)
	require.Zero(t, n, "notified once")

	ev := lastPayload[events.ScheduleReminderDue](t, pg, events.TypeScheduleReminderDue)
	require.Len(t, ev.UserIDs, 3)
	require.Equal(t, ch.ID, ev.ChannelID)
	require.Equal(t, "Rank tối", ev.Title)
}
