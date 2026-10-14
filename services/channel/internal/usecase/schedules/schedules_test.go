package schedules_test

import (
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/schedules"
	"github.com/stretchr/testify/require"
)

const _cat = "0199c0de-0000-7000-8000-000000000001"

type fakeRepo struct {
	repo.ChannelRepo
	repo.ScheduleRepo
	repo.ReminderRepo
	repo.CategoryRepo

	schedules map[string]entity.Schedule
	active    int
	live      bool
	added     []entity.Reminder
	announced []events.ScheduleOccurrenceUpcoming
}

func (f *fakeRepo) ByOwner(_ context.Context, owner string) (entity.Channel, error) {
	if owner != "u1" {
		return entity.Channel{}, entity.ErrNotFound
	}

	return entity.Channel{ID: "c1", OwnerID: "u1"}, nil
}

func (f *fakeRepo) ByIDs(_ context.Context, ids []string) (map[string]entity.Category, error) {
	out := map[string]entity.Category{}

	for _, id := range ids {
		if id == _cat {
			out[id] = entity.Category{ID: id, Slug: "valorant"}
		}
	}

	return out, nil
}

func (f *fakeRepo) Create(_ context.Context, s entity.Schedule, maxActive int, _ time.Time) (entity.Schedule, error) {
	if f.active >= maxActive {
		return entity.Schedule{}, entity.ErrScheduleLimit
	}

	s.ID = "new"
	f.schedules[s.ID] = s

	return s, nil
}

func (f *fakeRepo) Get(_ context.Context, channelID, id string) (entity.Schedule, error) {
	s, ok := f.schedules[id]
	if !ok || s.ChannelID != channelID {
		return entity.Schedule{}, entity.ErrNotFound
	}

	return s, nil
}

func (f *fakeRepo) GetPublic(_ context.Context, id string) (entity.Schedule, error) {
	s, ok := f.schedules[id]
	if !ok {
		return entity.Schedule{}, entity.ErrNotFound
	}

	return s, nil
}

func (f *fakeRepo) OccurrenceLive(context.Context, string, time.Time) (bool, error) {
	return f.live, nil
}

func (f *fakeRepo) MoveOccurrence(_ context.Context, _ entity.Schedule, _ time.Time, moved entity.Schedule) (entity.Schedule, error) {
	moved.ID = "moved"

	return moved, nil
}

func (f *fakeRepo) CancelOccurrence(context.Context, entity.Schedule, time.Time) error { return nil }

func (f *fakeRepo) Counts(context.Context, []string, time.Time) (map[entity.OccurrenceKey]int, error) {
	return map[entity.OccurrenceKey]int{}, nil
}

func (f *fakeRepo) OccurrenceStreams(context.Context, []string) (map[entity.OccurrenceKey]string, error) {
	return map[entity.OccurrenceKey]string{}, nil
}

func (f *fakeRepo) Add(_ context.Context, r entity.Reminder) (entity.Reminder, bool, error) {
	f.added = append(f.added, r)

	return r, true, nil
}

func (f *fakeRepo) AnnounceDue(_ context.Context, _ time.Time, _ int, fn func(entity.Schedule) []events.ScheduleOccurrenceUpcoming) (int, error) {
	for _, s := range f.schedules {
		f.announced = append(f.announced, fn(s)...)
	}

	return len(f.announced), nil
}

func now() time.Time { return time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC) }

func newUC(f *fakeRepo) *schedules.UseCase {
	l := logger.New("error")

	return schedules.New(schedules.Repos{Channels: f, Schedules: f, Reminders: f, Categories: f}, enrich.New(f, nil, l),
		schedules.Config{
			MaxActive: 20, Horizon: 90 * 24 * time.Hour, UpcomingLead: 24 * time.Hour, ReminderLead: 10 * time.Minute,
			DefaultTimezone: "Asia/Ho_Chi_Minh",
		}, l).WithClock(now)
}

func series(t *testing.T, start time.Time) entity.Schedule {
	t.Helper()

	r, ok := entity.ParseRRule("FREQ=WEEKLY;BYDAY=" + []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}[start.Weekday()])
	require.True(t, ok)

	return entity.Schedule{
		ID: "s1", ChannelID: "c1", Title: "Rank", StartAt: start, DurationMin: 60, RRule: &r, Timezone: "UTC",
		Status: entity.ScheduleActive,
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()

	start := now().Add(48 * time.Hour)
	bad := "0199c0de-0000-7000-8000-0000000000ff"
	cat := _cat

	tests := []struct {
		name    string
		owner   string
		in      entity.ScheduleInput
		active  int
		wantErr error
		details map[string]string
	}{
		{name: "created", owner: "u1", in: entity.ScheduleInput{Title: "x", StartAt: start, DurationMin: 60, CategoryID: &cat}},
		{name: "not a creator", owner: "u9", in: entity.ScheduleInput{Title: "x", StartAt: start, DurationMin: 60}, wantErr: entity.ErrNotCreator},
		{
			name: "21st schedule", owner: "u1", active: 20, in: entity.ScheduleInput{Title: "x", StartAt: start, DurationMin: 60},
			wantErr: entity.ErrScheduleLimit, details: map[string]string{"limit": "20"},
		},
		{
			name: "unknown category", owner: "u1", in: entity.ScheduleInput{Title: "x", StartAt: start, DurationMin: 60, CategoryID: &bad},
			wantErr: entity.ErrValidation, details: map[string]string{"categoryId": entity.DetailInvalid},
		},
		{
			name: "91 days ahead", owner: "u1", in: entity.ScheduleInput{Title: "x", StartAt: now().Add(91 * 24 * time.Hour), DurationMin: 60},
			wantErr: entity.ErrValidation, details: map[string]string{"startAt": entity.DetailMax},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &fakeRepo{schedules: map[string]entity.Schedule{}, active: tc.active}
			v, err := newUC(f).Create(context.Background(), tc.owner, tc.in)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, tc.details, entity.DetailsOf(err))

				return
			}

			require.NoError(t, err)
			require.Equal(t, "valorant", v.Category.Slug)
			require.Equal(t, "Asia/Ho_Chi_Minh", v.Schedule.Timezone)
			require.Len(t, v.Next, 1)
		})
	}
}

func TestOccurrenceChanges(t *testing.T) {
	t.Parallel()

	start := now().Add(2 * time.Hour)
	cancelled := series(t, start)
	cancelled.ID, cancelled.Status = "s2", entity.ScheduleCancelled
	f := &fakeRepo{schedules: map[string]entity.Schedule{"s1": series(t, start), "s2": cancelled}}
	uc := newUC(f)
	ctx := context.Background()

	v, err := uc.MoveOccurrence(ctx, "u1", "s1", start, start.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "s1", *v.Schedule.ParentID)
	require.Nil(t, v.Schedule.RRule, "a moved occurrence is a one-off")

	require.ErrorIs(t, uc.CancelOccurrence(ctx, "u1", "s1", start.Add(time.Hour)), entity.ErrNotFound, "not an occurrence")
	require.ErrorIs(t, uc.CancelOccurrence(ctx, "u1", "s1", start.AddDate(0, 0, -7)), entity.ErrNotFound, "before the series")
	require.ErrorIs(t, uc.CancelOccurrence(ctx, "u1", "s2", start), entity.ErrInvalidState, "cancelled schedule")
	require.ErrorIs(t, uc.CancelOccurrence(ctx, "u9", "s1", start), entity.ErrNotCreator)

	_, err = uc.MoveOccurrence(ctx, "u1", "s1", start, now().Add(-time.Minute))
	require.ErrorIs(t, err, entity.ErrValidation, "moved into the past")

	f.live = true

	require.ErrorIs(t, uc.CancelOccurrence(ctx, "u1", "s1", start), entity.ErrInvalidState, "on air")

	past := series(t, now().Add(-24*time.Hour))
	f.schedules["s3"] = past
	require.ErrorIs(t, uc.CancelOccurrence(ctx, "u1", "s3", past.StartAt), entity.ErrInvalidState, "already happened")
}

func TestRemind(t *testing.T) {
	t.Parallel()

	start := now().Add(2 * time.Hour)
	cancelled := series(t, start)
	cancelled.ID, cancelled.Status = "s2", entity.ScheduleCancelled
	f := &fakeRepo{schedules: map[string]entity.Schedule{"s1": series(t, start), "s2": cancelled, "s3": series(t, now().Add(-time.Hour))}}
	uc := newUC(f)
	ctx := context.Background()

	_, created, err := uc.Remind(ctx, "v1", "s1", start.AddDate(0, 0, 7))
	require.NoError(t, err)
	require.True(t, created)

	_, _, err = uc.Remind(ctx, "v1", "s2", start)
	require.ErrorIs(t, err, entity.ErrInvalidState, "cancelled schedule")

	_, _, err = uc.Remind(ctx, "v1", "s3", now().Add(-time.Hour))
	require.ErrorIs(t, err, entity.ErrInvalidState, "past occurrence")

	_, _, err = uc.Remind(ctx, "v1", "s1", start.Add(time.Minute))
	require.ErrorIs(t, err, entity.ErrNotFound, "not an occurrence")

	_, _, err = uc.Remind(ctx, "v1", "nope", start)
	require.ErrorIs(t, err, entity.ErrNotFound)

	require.Len(t, f.added, 1)
}

func TestAnnounceUpcoming(t *testing.T) {
	t.Parallel()

	start := now().Add(2 * time.Hour)
	// Its next occurrence (in 23 h 30) was announced by an earlier run.
	announced := series(t, now().Add(-7*24*time.Hour+23*time.Hour+30*time.Minute))
	announced.ID = "s2"
	watermark := now().Add(23*time.Hour + 45*time.Minute)
	announced.UpcomingNotifiedUntil = &watermark

	f := &fakeRepo{schedules: map[string]entity.Schedule{"s1": series(t, start), "s2": announced}}

	n, err := newUC(f).AnnounceUpcoming(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, "s1", f.announced[0].ScheduleID)
	require.Equal(t, start, f.announced[0].OccurrenceAt, "within 24 h; next week's is not")
}
