package entity

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ScheduleStatus -.
type ScheduleStatus string

// Schedule statuses.
const (
	ScheduleActive    ScheduleStatus = "ACTIVE"
	ScheduleCancelled ScheduleStatus = "CANCELLED"
)

// Valid -.
func (s ScheduleStatus) Valid() bool {
	return s == ScheduleActive || s == ScheduleCancelled
}

// Schedule limits (NXL-G2 BR-G2-11…16).
const (
	ScheduleTitleMax   = 140
	ScheduleMinMinutes = 15
	ScheduleMaxMinutes = 720
	RRuleCountMax      = 100
	// NextOccurrencesMax is how many upcoming occurrences a schedule shows.
	NextOccurrencesMax = 5
	// ScheduleRangeMax bounds GET /channels/{id}/schedule.
	ScheduleRangeMax = 31 * _day
	// _maxScanDays stops the day walk of a series whatever its rule.
	_maxScanDays = 20000
	_untilLayout = "20060102T150405Z"
	// _lastScanMax: a bounded series has at most COUNT (100) or ~90 days × 7 occurrences.
	_lastScanMax = 1000
)

// RRule is the supported subset of RFC 5545 (G4-Q5): FREQ=WEEKLY;BYDAY=… with an optional COUNT or UNTIL.
type RRule struct {
	Days  [7]bool
	Count int
	Until *time.Time
}

// dayCodes are the BYDAY codes, Monday first (the canonical order of String).
func dayCodes() []string {
	return []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}
}

func weekdayOf(code string) (time.Weekday, bool) {
	i := slices.Index(dayCodes(), code)
	if i < 0 {
		return 0, false
	}

	return time.Weekday((i + 1) % len(dayCodes())), true
}

// ParseRRule accepts the subset only; anything else (DAILY, INTERVAL, BYSETPOS …) is refused.
func ParseRRule(s string) (RRule, bool) {
	var (
		r    RRule
		freq bool
	)

	for _, part := range strings.Split(strings.ToUpper(strings.TrimSpace(s)), ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return RRule{}, false
		}

		if k == "FREQ" {
			freq = v == "WEEKLY"

			continue
		}

		if !r.apply(k, v) {
			return RRule{}, false
		}
	}

	if !freq || r.Days == [7]bool{} || (r.Count > 0 && r.Until != nil) {
		return RRule{}, false
	}

	return r, true
}

// apply sets one BYDAY, COUNT or UNTIL part; false for anything else or a repeated part.
func (r *RRule) apply(k, v string) bool {
	switch k {
	case "BYDAY":
		return r.Days == [7]bool{} && r.parseDays(v)
	case "COUNT":
		return r.setCount(v)
	case "UNTIL":
		return r.setUntil(v)
	}

	return false
}

func (r *RRule) setCount(v string) bool {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > RRuleCountMax || r.Count != 0 {
		return false
	}

	r.Count = n

	return true
}

func (r *RRule) setUntil(v string) bool {
	t, err := time.Parse(_untilLayout, v)
	if err != nil || r.Until != nil {
		return false
	}

	r.Until = &t

	return true
}

func (r *RRule) parseDays(v string) bool {
	if v == "" {
		return false
	}

	for _, d := range strings.Split(v, ",") {
		wd, ok := weekdayOf(d)
		if !ok {
			return false
		}

		r.Days[wd] = true
	}

	return true
}

// String is the canonical form (days Monday first).
func (r RRule) String() string {
	days := make([]string, 0, len(dayCodes()))

	for _, d := range dayCodes() {
		if wd, _ := weekdayOf(d); r.Days[wd] {
			days = append(days, d)
		}
	}

	s := "FREQ=WEEKLY;BYDAY=" + strings.Join(days, ",")

	switch {
	case r.Count > 0:
		s += ";COUNT=" + strconv.Itoa(r.Count)
	case r.Until != nil:
		s += ";UNTIL=" + r.Until.UTC().Format(_untilLayout)
	}

	return s
}

// Schedule is a one-off broadcast or a weekly series (schedules).
type Schedule struct {
	ID          string
	ChannelID   string
	CategoryID  *string
	Title       string
	StartAt     time.Time
	DurationMin int
	// RRule is nil for a one-off.
	RRule                 *RRule
	Timezone              string
	Status                ScheduleStatus
	Exdates               []time.Time
	ParentID              *string
	LastOccurrenceAt      *time.Time
	UpcomingNotifiedUntil *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Location is the schedule's time zone (UTC when unknown).
func (s Schedule) Location() *time.Location {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.UTC
	}

	return loc
}

// Occurrences returns the start instants (UTC) with from <= t < to, at most limit of them. A weekly occurrence
// keeps the local wall-clock time of StartAt in the schedule's time zone; COUNT includes excluded dates.
func (s Schedule) Occurrences(from, to time.Time, limit int) []time.Time {
	if limit <= 0 {
		return nil
	}

	if s.RRule == nil {
		if !s.StartAt.Before(from) && s.StartAt.Before(to) && !s.excluded(s.StartAt) {
			return []time.Time{s.StartAt.UTC()}
		}

		return nil
	}

	var out []time.Time

	s.walk(to, func(day time.Time) bool {
		if !day.Before(from) && !s.excluded(day) {
			out = append(out, day.UTC())
		}

		return len(out) < limit
	})

	return out
}

// walk calls fn with each occurrence of the weekly series in order (excluded dates included) until fn returns
// false, to is reached or the rule ends.
func (s Schedule) walk(to time.Time, fn func(time.Time) bool) {
	loc := s.Location()
	start := s.StartAt.In(loc)
	y, m, d := start.Date()
	hh, mm, ss := start.Clock()
	n := 0

	for i := range _maxScanDays {
		day := time.Date(y, m, d+i, hh, mm, ss, 0, loc)
		if !day.Before(to) || s.RRule.ended(day, n) {
			return
		}

		if !s.RRule.Days[day.Weekday()] {
			continue
		}

		n++

		if !fn(day) {
			return
		}
	}
}

// ended reports whether the series is over at day once n occurrences were produced.
func (r RRule) ended(day time.Time, n int) bool {
	return (r.Until != nil && day.After(*r.Until)) || (r.Count > 0 && n >= r.Count)
}

func (s Schedule) excluded(t time.Time) bool {
	return slices.ContainsFunc(s.Exdates, t.Equal)
}

// HasOccurrence reports whether at is a (not excluded) occurrence.
func (s Schedule) HasOccurrence(at time.Time) bool {
	occ := s.Occurrences(at, at.Add(time.Second), 1)

	return len(occ) == 1 && occ[0].Equal(at)
}

// Last is the last occurrence of a bounded series (nil for an open-ended one).
func (s Schedule) Last() *time.Time {
	switch {
	case s.RRule == nil:
		t := s.StartAt.UTC()

		return &t
	case s.RRule.Count == 0 && s.RRule.Until == nil:
		return nil
	}

	end := s.StartAt.Add(_maxScanDays * _day)
	if s.RRule.Until != nil {
		end = s.RRule.Until.Add(time.Second)
	}

	all := Schedule{StartAt: s.StartAt, RRule: s.RRule, Timezone: s.Timezone}.Occurrences(s.StartAt, end, _lastScanMax)
	if len(all) == 0 {
		t := s.StartAt.UTC()

		return &t
	}

	return &all[len(all)-1]
}

// ScheduleInput is a schedule as the creator submits it.
type ScheduleInput struct {
	Title       string
	CategoryID  *string
	StartAt     time.Time
	DurationMin int
	// RRule is "" for a one-off.
	RRule    string
	Timezone string
}

// ScheduleRules are the limits a new schedule is checked against.
type ScheduleRules struct {
	Now             time.Time
	Horizon         time.Duration
	DefaultTimezone string
	// KeepStart skips the MIN check when StartAt is unchanged (editing a series that already started).
	KeepStart *time.Time
}

// Build validates in and returns the schedule it describes, or the VALIDATION_FAILED details.
func (in ScheduleInput) Build(r ScheduleRules) (s Schedule, errs map[string]string) {
	s = Schedule{
		Title: strings.TrimSpace(in.Title), StartAt: in.StartAt.UTC(), DurationMin: in.DurationMin,
		Timezone: strings.TrimSpace(in.Timezone), Status: ScheduleActive,
	}
	errs = s.checkBasics()

	if s.Timezone == "" {
		s.Timezone = r.DefaultTimezone
	}

	loc, ok := location(s.Timezone)
	if !ok {
		errs["timezone"] = DetailFormat
	}

	if d := in.checkStart(r); d != "" {
		errs["startAt"] = d
	}

	if in.CategoryID != nil {
		u, uerr := uuid.Parse(*in.CategoryID)
		if uerr != nil {
			errs["categoryId"] = DetailInvalid
		}

		id := u.String()
		s.CategoryID = &id
	}

	if in.RRule != "" {
		rule, ok := ParseRRule(in.RRule)
		if !ok || !rule.Days[s.StartAt.In(loc).Weekday()] || !untilInRange(rule, s.StartAt, r) {
			errs["rrule"] = DetailFormat
		}

		s.RRule = &rule
	}

	s.LastOccurrenceAt = s.Last()

	return s, errs
}

// location loads an IANA zone; "Local" (the server's zone) is refused.
func location(name string) (loc *time.Location, ok bool) {
	loc, err := time.LoadLocation(name)
	if err != nil || name == "Local" {
		return time.UTC, false
	}

	return loc, true
}

func (s Schedule) checkBasics() map[string]string {
	errs := map[string]string{}

	if n := utf8.RuneCountInString(s.Title); n == 0 || n > ScheduleTitleMax {
		errs["title"] = DetailLength
	}

	switch {
	case s.DurationMin < ScheduleMinMinutes:
		errs["durationMin"] = DetailMin
	case s.DurationMin > ScheduleMaxMinutes:
		errs["durationMin"] = DetailMax
	}

	return errs
}

func (in ScheduleInput) checkStart(r ScheduleRules) string {
	if r.KeepStart != nil && in.StartAt.Equal(*r.KeepStart) {
		return ""
	}

	switch {
	case !in.StartAt.After(r.Now):
		return DetailMin
	case in.StartAt.After(r.Now.Add(r.Horizon)):
		return DetailMax
	}

	return ""
}

func untilInRange(rule RRule, start time.Time, r ScheduleRules) bool {
	return rule.Until == nil || (!rule.Until.Before(start) && !rule.Until.After(r.Now.Add(r.Horizon)))
}

// SameTiming reports whether s and o produce the same occurrences (start, rule, time zone).
func (s Schedule) SameTiming(o Schedule) bool {
	sameRule := (s.RRule == nil) == (o.RRule == nil) && (s.RRule == nil || s.RRule.String() == o.RRule.String())

	return sameRule && s.StartAt.Equal(o.StartAt) && s.Timezone == o.Timezone
}

// OccurrenceKey identifies one occurrence; At is UnixMicro so that equal instants compare equal.
type OccurrenceKey struct {
	ScheduleID string
	At         int64
}

// KeyOf -.
func KeyOf(scheduleID string, at time.Time) OccurrenceKey {
	return OccurrenceKey{ScheduleID: scheduleID, At: at.UnixMicro()}
}

// NextOccurrence is an upcoming occurrence shown in the Studio.
type NextOccurrence struct {
	At            time.Time
	ReminderCount int
	StreamID      *string
}

// ScheduleView is a schedule with what the Studio shows next to it.
type ScheduleView struct {
	Schedule Schedule
	Category *Category
	Next     []NextOccurrence
}

// OccurrenceView is one occurrence on a channel's Schedule tab.
type OccurrenceView struct {
	ScheduleID   string
	OccurrenceAt time.Time
	Title        string
	Category     *Category
	DurationMin  int
	StreamID     *string
	// Reminded is nil for guests.
	Reminded *bool
}

// Reminder is a viewer's request to be reminded of an occurrence.
type Reminder struct {
	UserID       string
	ScheduleID   string
	OccurrenceAt time.Time
	CreatedAt    time.Time
}
