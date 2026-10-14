package entity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/stretchr/testify/require"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation(name)
	require.NoError(t, err)

	return loc
}

func TestParseRRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		ok   bool
		want string
	}{
		{in: "FREQ=WEEKLY;BYDAY=MO,WE", ok: true, want: "FREQ=WEEKLY;BYDAY=MO,WE"},
		{in: "freq=weekly;byday=su,mo", ok: true, want: "FREQ=WEEKLY;BYDAY=MO,SU"},
		{in: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU;COUNT=10", ok: true, want: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU;COUNT=10"},
		{in: "FREQ=WEEKLY;BYDAY=FR;UNTIL=20261231T170000Z", ok: true, want: "FREQ=WEEKLY;BYDAY=FR;UNTIL=20261231T170000Z"},
		{in: "FREQ=DAILY"},
		{in: "FREQ=WEEKLY"},
		{in: "FREQ=WEEKLY;BYDAY=XX"},
		{in: "FREQ=WEEKLY;BYDAY=MO;INTERVAL=2"},
		{in: "FREQ=WEEKLY;BYDAY=MO;BYSETPOS=1"},
		{in: "FREQ=WEEKLY;BYDAY=MO;COUNT=0"},
		{in: "FREQ=WEEKLY;BYDAY=MO;COUNT=101"},
		{in: "FREQ=WEEKLY;BYDAY=MO;COUNT=2;UNTIL=20261231T170000Z"},
		{in: "FREQ=WEEKLY;BYDAY=MO;BYDAY=TU"},
		{in: "FREQ=WEEKLY;BYDAY=MO;UNTIL=2026-12-31"},
		{in: ""},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			r, ok := entity.ParseRRule(tc.in)
			require.Equal(t, tc.ok, ok)

			if ok {
				require.Equal(t, tc.want, r.String())
			}
		})
	}
}

func weekly(t *testing.T, start time.Time, rule, tz string) entity.Schedule {
	t.Helper()

	r, ok := entity.ParseRRule(rule)
	require.True(t, ok, rule)

	return entity.Schedule{ID: "s", StartAt: start.UTC(), RRule: &r, Timezone: tz}
}

func TestOccurrences(t *testing.T) {
	t.Parallel()

	hcm, tokyo, ny := zone(t, "Asia/Ho_Chi_Minh"), zone(t, "Asia/Tokyo"), zone(t, "America/New_York")
	mon20 := time.Date(2026, 10, 19, 20, 0, 0, 0, hcm) // a Monday, 20:00 in Vietnam
	from, to := mon20.Add(-3*time.Hour), mon20.Add(15*24*time.Hour)

	tests := []struct {
		name string
		s    entity.Schedule
		want []time.Time
	}{
		{name: "one-off", s: entity.Schedule{StartAt: mon20.UTC()}, want: []time.Time{mon20.UTC()}},
		{name: "one-off excluded", s: entity.Schedule{StartAt: mon20.UTC(), Exdates: []time.Time{mon20}}},
		{name: "two days a week", s: weekly(t, mon20, "FREQ=WEEKLY;BYDAY=MO,WE", "Asia/Ho_Chi_Minh"), want: []time.Time{
			mon20, mon20.AddDate(0, 0, 2), mon20.AddDate(0, 0, 7), mon20.AddDate(0, 0, 9), mon20.AddDate(0, 0, 14),
		}},
		{name: "count includes excluded dates", s: func() entity.Schedule {
			s := weekly(t, mon20, "FREQ=WEEKLY;BYDAY=MO;COUNT=2", "Asia/Ho_Chi_Minh")
			s.Exdates = []time.Time{mon20.UTC()}

			return s
		}(), want: []time.Time{mon20.AddDate(0, 0, 7)}},
		{name: "until is inclusive", s: weekly(t, mon20, "FREQ=WEEKLY;BYDAY=MO;UNTIL="+mon20.AddDate(0, 0, 7).UTC().Format("20060102T150405Z"),
			"Asia/Ho_Chi_Minh"), want: []time.Time{mon20, mon20.AddDate(0, 0, 7)}},
		// 20:00 in Tokyo on Monday is 18:00 in Vietnam (AC-D19: viewers see their own time).
		{
			name: "time zone keeps the wall clock", s: weekly(t, time.Date(2026, 10, 19, 20, 0, 0, 0, tokyo), "FREQ=WEEKLY;BYDAY=MO", "Asia/Tokyo"),
			want: []time.Time{mon20.Add(-2 * time.Hour), mon20.Add(-2*time.Hour).AddDate(0, 0, 7), mon20.Add(-2*time.Hour).AddDate(0, 0, 14)},
		},
		// 06:30 on Tuesday in Vietnam is 23:30 on Monday in UTC: BYDAY follows the schedule's zone.
		{
			name: "midnight UTC boundary", s: weekly(t, time.Date(2026, 10, 20, 6, 30, 0, 0, hcm), "FREQ=WEEKLY;BYDAY=TU", "Asia/Ho_Chi_Minh"),
			want: []time.Time{
				time.Date(2026, 10, 19, 23, 30, 0, 0, time.UTC), time.Date(2026, 10, 26, 23, 30, 0, 0, time.UTC),
				time.Date(2026, 11, 2, 23, 30, 0, 0, time.UTC),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := tc.s.Occurrences(from, to, 10)
			require.Len(t, got, len(tc.want))

			for i := range tc.want {
				require.True(t, tc.want[i].Equal(got[i]), "%d: want %s got %s", i, tc.want[i].UTC(), got[i])
			}
		})
	}

	// DST: 19:00 in New York stays 19:00 local, so the UTC instant moves by an hour on 1 November.
	dst := weekly(t, time.Date(2026, 10, 25, 19, 0, 0, 0, ny), "FREQ=WEEKLY;BYDAY=SU", "America/New_York")
	got := dst.Occurrences(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), 5)
	require.Equal(t, []time.Time{time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}, got)

	require.Empty(t, dst.Occurrences(from, to, 0))
	require.True(t, weekly(t, mon20, "FREQ=WEEKLY;BYDAY=MO", "Asia/Ho_Chi_Minh").HasOccurrence(mon20.AddDate(0, 0, 7).UTC()))
	require.False(t, weekly(t, mon20, "FREQ=WEEKLY;BYDAY=MO", "Asia/Ho_Chi_Minh").HasOccurrence(mon20.AddDate(0, 0, 8).UTC()))
}

func TestLast(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 19, 13, 0, 0, 0, time.UTC)

	require.Nil(t, weekly(t, start, "FREQ=WEEKLY;BYDAY=MO", "UTC").Last(), "open-ended")
	require.Equal(t, start.AddDate(0, 0, 14), *weekly(t, start, "FREQ=WEEKLY;BYDAY=MO;COUNT=3", "UTC").Last())
	require.Equal(t, start.AddDate(0, 0, 7), *weekly(t, start, "FREQ=WEEKLY;BYDAY=MO;UNTIL=20261030T000000Z", "UTC").Last())
	require.Equal(t, start, *entity.Schedule{StartAt: start}.Last())
}

func TestScheduleBuild(t *testing.T) { //nolint:funlen // one table of rules
	t.Parallel()

	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	rules := entity.ScheduleRules{Now: now, Horizon: 90 * 24 * time.Hour, DefaultTimezone: "Asia/Ho_Chi_Minh"}
	start := time.Date(2026, 10, 19, 13, 0, 0, 0, time.UTC) // Monday 20:00 in Vietnam
	ok := entity.ScheduleInput{Title: " Rank ", StartAt: start, DurationMin: 120, RRule: "FREQ=WEEKLY;BYDAY=MO"}
	cat := "0199c0de-0000-7000-8000-000000000001"

	tests := []struct {
		name string
		edit func(in *entity.ScheduleInput, r *entity.ScheduleRules)
		want map[string]string
	}{
		{name: "valid", edit: func(*entity.ScheduleInput, *entity.ScheduleRules) {}, want: map[string]string{}},
		{
			name: "empty title", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.Title = "  " },
			want: map[string]string{"title": entity.DetailLength},
		},
		{
			name: "long title", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.Title = strings.Repeat("á", 141) },
			want: map[string]string{"title": entity.DetailLength},
		},
		{
			name: "past start", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.StartAt, in.RRule = now, "" },
			want: map[string]string{"startAt": entity.DetailMin},
		},
		{name: "past start kept on edit", edit: func(in *entity.ScheduleInput, r *entity.ScheduleRules) {
			in.StartAt = now.Add(-time.Hour)
			r.KeepStart = &in.StartAt
		}, want: map[string]string{"rrule": entity.DetailFormat}},
		{name: "beyond the horizon", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) {
			in.StartAt, in.RRule = now.Add(91*24*time.Hour), ""
		}, want: map[string]string{"startAt": entity.DetailMax}},
		{
			name: "too short", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.DurationMin = 14 },
			want: map[string]string{"durationMin": entity.DetailMin},
		},
		{
			name: "too long", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.DurationMin = 721 },
			want: map[string]string{"durationMin": entity.DetailMax},
		},
		{
			name: "unknown zone", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.Timezone = "Mars/Base" },
			want: map[string]string{"timezone": entity.DetailFormat},
		},
		{
			name: "server zone refused", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.Timezone = "Local" },
			want: map[string]string{"timezone": entity.DetailFormat},
		},
		{
			name: "byday misses the start day", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.RRule = "FREQ=WEEKLY;BYDAY=TU" },
			want: map[string]string{"rrule": entity.DetailFormat},
		},
		{name: "byday in the schedule zone", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) {
			in.Timezone, in.RRule = "America/Los_Angeles", "FREQ=WEEKLY;BYDAY=MO" // Monday 06:00 there
		}, want: map[string]string{}},
		{name: "until beyond the horizon", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) {
			in.RRule = "FREQ=WEEKLY;BYDAY=MO;UNTIL=20270601T000000Z"
		}, want: map[string]string{"rrule": entity.DetailFormat}},
		{name: "category not a uuid", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) {
			bad := "lmht"
			in.CategoryID = &bad
		}, want: map[string]string{"categoryId": entity.DetailInvalid}},
		{name: "category", edit: func(in *entity.ScheduleInput, _ *entity.ScheduleRules) { in.CategoryID = &cat }, want: map[string]string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in, r := ok, rules
			tc.edit(&in, &r)

			s, errs := in.Build(r)
			require.Equal(t, tc.want, errs)

			if len(errs) == 0 {
				require.Equal(t, strings.TrimSpace(in.Title), s.Title)
				require.NotEmpty(t, s.Timezone)
			}
		})
	}

	s, errs := ok.Build(rules)
	require.Empty(t, errs)
	require.Equal(t, "Asia/Ho_Chi_Minh", s.Timezone)
	require.Nil(t, s.LastOccurrenceAt, "open-ended series")
	require.False(t, s.SameTiming(entity.Schedule{StartAt: start, Timezone: s.Timezone}))
	require.True(t, s.SameTiming(s))
}
