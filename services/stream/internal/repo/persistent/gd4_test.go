package persistent_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

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

func upcoming(channelID, scheduleID string, at time.Time) events.ScheduleOccurrenceUpcoming {
	return events.ScheduleOccurrenceUpcoming{ScheduleID: scheduleID, ChannelID: channelID, Title: "Lịch rank", OccurrenceAt: at, DurationMin: 60}
}

func window() repo.AuthorizeParams {
	return repo.AuthorizeParams{Grace: time.Minute, EarlyStart: 30 * time.Minute, LateGrace: time.Hour}
}

func TestScheduledAttach(t *testing.T) { //nolint:paralleltest,funlen // shared database; one story
	r, pg := setup(t)
	ctx := context.Background()
	ch, sched := uuid.NewString(), uuid.NewString()
	cat := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)

	// Two occurrences inside the window: the one closest to now wins (AF-G2-09).
	near, far := now.Add(10*time.Minute), now.Add(-50*time.Minute)
	ev := uuid.NewString()
	require.NoError(t, r.CreateScheduled(ctx, ev, upcoming(ch, sched, near)))
	require.NoError(t, r.CreateScheduled(ctx, ev, upcoming(ch, sched, near)), "redelivery")
	require.NoError(t, r.CreateScheduled(ctx, uuid.NewString(), upcoming(ch, sched, near)), "announced twice: unique occurrence")
	require.NoError(t, r.CreateScheduled(ctx, uuid.NewString(), upcoming(ch, uuid.NewString(), far)))

	scheduled := lastPayload[events.StreamScheduled](t, pg, events.TypeStreamScheduled)
	require.Equal(t, 2, len(outboxTypes(t, pg)), "one stream.scheduled per occurrence")

	s, err := r.Get(ctx, scheduled.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusScheduled, s.Status)
	require.True(t, far.Equal(*s.ScheduledStartAt))

	require.NoError(t, r.SaveSettings(ctx, entity.Settings{ChannelID: ch, Title: "", Tags: []string{"fps"}, Language: "vi", CategoryID: &cat}))

	g, err := r.Authorize(ctx, ch, window())
	require.NoError(t, err)

	live, err := r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusLive, live.Status)
	require.Equal(t, sched, *live.ScheduleID, "the nearest occurrence was attached (same stream id)")
	require.Equal(t, "Lịch rank", live.Title, "empty settings title keeps the schedule's")
	require.Equal(t, cat, *live.CategoryID)

	started := lastPayload[events.StreamStarted](t, pg, events.TypeStreamStarted)
	require.Equal(t, g.StreamID, started.StreamID)
	require.Equal(t, sched, *started.ScheduleID)
	require.Equal(t, cat, *started.CategoryID)

	// The far one is still waiting, and is cancelled once more than an hour late.
	_, err = pg.Pool.Exec(ctx, `UPDATE streams SET scheduled_start_at = now() - interval '61 minutes' WHERE id = $1`, scheduled.StreamID)
	require.NoError(t, err)

	n, err := r.CancelLateScheduled(ctx, time.Hour, 10)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	cancelled := lastPayload[events.StreamStatusChanged](t, pg, events.TypeStreamStatusChanged)
	require.Equal(t, events.StreamStatusCancelled, cancelled.Status)
	require.Equal(t, scheduled.StreamID, cancelled.StreamID)

	n, err = r.CancelLateScheduled(ctx, time.Hour, 10)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestScheduledWindowEdges(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()

	tests := []struct {
		name   string
		offset time.Duration
		attach bool
	}{
		{name: "29 minutes early", offset: 29 * time.Minute, attach: true},
		{name: "31 minutes early", offset: 31 * time.Minute},
		{name: "59 minutes late", offset: -59 * time.Minute, attach: true},
		{name: "61 minutes late", offset: -61 * time.Minute},
	}

	for _, tc := range tests {
		ch, sched := uuid.NewString(), uuid.NewString()
		require.NoError(t, r.CreateScheduled(ctx, "", upcoming(ch, sched, now.Add(tc.offset))))

		g, err := r.Authorize(ctx, ch, window())
		require.NoError(t, err)

		s, err := r.Get(ctx, g.StreamID)
		require.NoError(t, err)
		require.Equal(t, tc.attach, s.ScheduleID != nil, tc.name)
	}
}

func TestScheduledChanges(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ch, sched, moved := uuid.NewString(), uuid.NewString(), uuid.NewString()
	at := time.Now().UTC().Add(5 * time.Hour).Truncate(time.Second)
	newAt := at.Add(time.Hour)

	require.NoError(t, r.CreateScheduled(ctx, "", upcoming(ch, sched, at)))
	id := lastPayload[events.StreamScheduled](t, pg, events.TypeStreamScheduled).StreamID

	require.NoError(t, r.RetitleScheduled(ctx, "", events.ScheduleUpdated{ScheduleID: sched, Title: "Đổi tên"}))
	require.NoError(t, r.ChangeScheduled(ctx, "", events.ScheduleOccurrenceChanged{
		ScheduleID: sched, OccurrenceAt: at,
		Reason: events.OccurrenceMoved, NewScheduleID: &moved, NewOccurrenceAt: &newAt,
	}))

	s, err := r.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Đổi tên", s.Title)
	require.Equal(t, moved, *s.ScheduleID, "moved, same stream id")
	require.True(t, newAt.Equal(*s.ScheduledStartAt))

	status := lastPayload[events.StreamStatusChanged](t, pg, events.TypeStreamStatusChanged)
	require.Equal(t, events.StreamStatusScheduled, status.Status)
	require.True(t, newAt.Equal(*status.ScheduledStartAt))

	// The job later announces the moved one-off: a no-op.
	require.NoError(t, r.CreateScheduled(ctx, "", upcoming(ch, moved, newAt)))

	require.NoError(t, r.ChangeScheduled(ctx, "", events.ScheduleOccurrenceChanged{
		ScheduleID: moved, OccurrenceAt: newAt,
		Reason: events.OccurrenceCancelled,
	}))

	s, err = r.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, entity.StatusCancelled, s.Status)

	// A cancelled occurrence can be scheduled again (series edited back to the same instant).
	require.NoError(t, r.CreateScheduled(ctx, "", upcoming(ch, moved, newAt)))
	require.NotEqual(t, id, lastPayload[events.StreamScheduled](t, pg, events.TypeStreamScheduled).StreamID)
}

// vod creates an ended stream with a READY VOD and returns the VOD.
func vod(t *testing.T, r *persistent.Repo, ch string, mature bool, cat *string) entity.VOD {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, r.SaveSettings(ctx, entity.Settings{
		ChannelID: ch, Title: "Rank", Tags: []string{}, Language: "vi", IsMature: mature,
		CategoryID: cat,
	}))

	g, err := r.Authorize(ctx, ch, window())
	require.NoError(t, err)

	_, err = r.End(ctx, ch, entity.EndCreator)
	require.NoError(t, err)

	require.NoError(t, r.OnVODProcessed(ctx, "", repo.VODInput{
		StreamID: g.StreamID, DurationSec: 60,
		PlaylistPath: "streams/" + g.StreamID + "/master.m3u8", ThumbnailURL: "streams/" + g.StreamID + "/thumb.jpg", Retention: time.Hour,
	}))

	s, err := r.Get(ctx, g.StreamID)
	require.NoError(t, err)

	v, err := r.GetVOD(ctx, *s.VODID)
	require.NoError(t, err)

	return v
}

func TestVODManagement(t *testing.T) { //nolint:paralleltest,funlen // shared database; one story
	r, pg := setup(t)
	ctx := context.Background()
	ch, other := uuid.NewString(), uuid.NewString()
	cat := uuid.NewString()

	a := vod(t, r, ch, false, &cat)
	b := vod(t, r, ch, true, nil)
	require.Equal(t, cat, *a.CategoryID, "the VOD keeps the stream's category")
	require.True(t, b.IsMature)

	ready := lastPayload[events.StreamVODReady](t, pg, events.TypeStreamVODReady)
	require.Equal(t, b.ID, ready.VODID)
	require.Equal(t, "Rank", ready.Title)
	require.True(t, ready.IsMature)
	require.False(t, ready.ExpiresAt.IsZero())

	page, err := r.ListPublic(ctx, repo.VODFilter{}, false, nil, 10)
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.Equal(t, b.ID, page[0].ID, "newest first")

	safe, err := r.ListPublic(ctx, repo.VODFilter{}, true, nil, 10)
	require.NoError(t, err)
	require.Len(t, safe, 1, "mature VODs are not recommended")

	byCat, err := r.ListPublic(ctx, repo.VODFilter{CategoryIDs: []string{cat}}, false, nil, 10)
	require.NoError(t, err)
	require.Len(t, byCat, 1)

	next, err := r.ListPublic(ctx, repo.VODFilter{}, false, &repo.VODCursor{CreatedAt: page[0].CreatedAt, ID: page[0].ID}, 10)
	require.NoError(t, err)
	require.Equal(t, []string{a.ID}, []string{next[0].ID})

	hidden := entity.VisibilityHidden
	_, err = r.UpdateVOD(ctx, other, a.ID, entity.VODPatch{Visibility: &hidden})
	require.ErrorIs(t, err, entity.ErrNotFound, "another channel's VOD")

	got, err := r.UpdateVOD(ctx, ch, a.ID, entity.VODPatch{Visibility: &hidden})
	require.NoError(t, err)
	require.True(t, got.Hidden())

	updated := lastPayload[events.VODUpdated](t, pg, events.TypeVODUpdated)
	require.Equal(t, entity.VisibilityHidden, updated.Visibility)

	public, err := r.ListByChannel(ctx, ch, true, repo.StudioVODFilter{}, nil, 10)
	require.NoError(t, err)
	require.Len(t, public, 1, "hidden VODs leave the public lists")

	studio, err := r.ListByChannel(ctx, ch, false, repo.StudioVODFilter{Visibility: &hidden}, nil, 10)
	require.NoError(t, err)
	require.Len(t, studio, 1)

	stats, err := r.DashboardStats(ctx, ch, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, entity.DashboardStats{StreamCount: 2, LiveSeconds: stats.LiveSeconds, VODReadyPublic: 1, VODReadyHidden: 1}, stats)

	recent, err := r.RecentEnded(ctx, ch, 5)
	require.NoError(t, err)
	require.Len(t, recent, 2)

	_, err = pg.Pool.Exec(ctx, `UPDATE vods SET status = 'EXPIRED' WHERE id = $1`, b.ID)
	require.NoError(t, err)

	title := "x"
	_, err = r.UpdateVOD(ctx, ch, b.ID, entity.VODPatch{Title: &title})
	require.ErrorIs(t, err, entity.ErrInvalidState, "an expired VOD cannot be edited")

	require.NoError(t, r.DeleteVOD(ctx, ch, b.ID), "an expired VOD can be deleted")
	require.ErrorIs(t, r.DeleteVOD(ctx, ch, b.ID), entity.ErrNotFound, "already deleted")

	deleted := lastPayload[events.VODDeleted](t, pg, events.TypeVODDeleted)
	require.Equal(t, b.StreamID, deleted.StreamID)
}

func TestListOnAirFilters(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	cat := uuid.NewString()
	chA, chB := uuid.NewString(), uuid.NewString()

	require.NoError(t, r.SaveSettings(ctx, entity.Settings{ChannelID: chA, Title: "A", Tags: []string{"fps", "rank"}, Language: "en", CategoryID: &cat}))
	require.NoError(t, r.SaveSettings(ctx, entity.Settings{ChannelID: chB, Title: "B", Tags: []string{"fps"}, Language: "vi"}))

	for _, ch := range []string{chA, chB} {
		_, err := r.Authorize(ctx, ch, window())
		require.NoError(t, err)
	}

	tests := []struct {
		name string
		f    repo.StreamFilter
		want int
	}{
		{name: "all", f: repo.StreamFilter{}, want: 2},
		{name: "category", f: repo.StreamFilter{CategoryIDs: []string{cat}}, want: 1},
		{name: "language", f: repo.StreamFilter{Language: "vi"}, want: 1},
		{name: "every tag", f: repo.StreamFilter{Tags: []string{"fps", "rank"}}, want: 1},
		{name: "shared tag", f: repo.StreamFilter{Tags: []string{"fps"}}, want: 2},
	}

	for _, tc := range tests {
		all, err := r.ListOnAirAll(ctx, tc.f, 100)
		require.NoError(t, err)
		require.Len(t, all, tc.want, tc.name)

		page, err := r.ListOnAirFiltered(ctx, tc.f, nil, 100)
		require.NoError(t, err)
		require.Len(t, page, tc.want, tc.name)
	}
}
