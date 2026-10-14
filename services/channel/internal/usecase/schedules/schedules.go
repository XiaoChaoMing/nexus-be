// Package schedules implements the creator's broadcast schedule, viewers' reminders and the jobs that
// announce upcoming occurrences and due reminders (NXL-G2 §3.4, NXL-D §3.8).
package schedules

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
)

const (
	_announceBatch = 100
	_reminderBatch = 1000
	// _announceMax bounds the occurrences of one schedule announced per run (a daily series has one a day).
	_announceMax = 50
	// _instant is the database's timestamp precision; it turns (from, until] into [from+1µs, until+1µs).
	_instant = time.Microsecond
)

// Repos groups the storage the use case needs.
type Repos struct {
	Channels   repo.ChannelRepo
	Schedules  repo.ScheduleRepo
	Reminders  repo.ReminderRepo
	Categories repo.CategoryRepo
}

// Config holds the schedule limits (NXL-00 §12).
type Config struct {
	MaxActive       int
	Horizon         time.Duration
	UpcomingLead    time.Duration
	ReminderLead    time.Duration
	DefaultTimezone string
}

// UseCase implements usecase.Schedules, usecase.Reminders and usecase.Jobs.
type UseCase struct {
	r      Repos
	enrich *enrich.Enricher
	cfg    Config
	l      logger.Interface
	now    func() time.Time
}

var (
	_ usecase.Schedules = (*UseCase)(nil)
	_ usecase.Reminders = (*UseCase)(nil)
	_ usecase.Jobs      = (*UseCase)(nil)
)

// New -.
func New(r Repos, e *enrich.Enricher, cfg Config, l logger.Interface) *UseCase {
	return &UseCase{r: r, enrich: e, cfg: cfg, l: l, now: time.Now}
}

// WithClock -.
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

func (uc *UseCase) channel(ctx context.Context, ownerID string) (entity.Channel, error) {
	ch, err := uc.r.Channels.ByOwner(ctx, ownerID)
	if errors.Is(err, entity.ErrNotFound) {
		return entity.Channel{}, entity.ErrNotCreator
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("ScheduleUseCase - channel: %w", err)
	}

	return ch, nil
}

// owned returns the caller's active schedule id: entity.ErrNotFound, or entity.ErrInvalidState when cancelled.
func (uc *UseCase) owned(ctx context.Context, ownerID, id string) (entity.Schedule, error) {
	ch, err := uc.channel(ctx, ownerID)
	if err != nil {
		return entity.Schedule{}, err
	}

	s, err := uc.r.Schedules.Get(ctx, ch.ID, id)
	if err != nil {
		return entity.Schedule{}, fmt.Errorf("ScheduleUseCase - schedule: %w", err)
	}

	if s.Status != entity.ScheduleActive {
		return entity.Schedule{}, entity.ErrInvalidState
	}

	return s, nil
}

// List -.
func (uc *UseCase) List(ctx context.Context, ownerID string, st entity.ScheduleStatus) ([]entity.ScheduleView, error) {
	ch, err := uc.channel(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	list, err := uc.r.Schedules.ListByChannel(ctx, ch.ID, st)
	if err != nil {
		return nil, fmt.Errorf("ScheduleUseCase - List: %w", err)
	}

	return uc.views(ctx, list)
}

func (uc *UseCase) rules(keepStart *time.Time) entity.ScheduleRules {
	return entity.ScheduleRules{Now: uc.now(), Horizon: uc.cfg.Horizon, DefaultTimezone: uc.cfg.DefaultTimezone, KeepStart: keepStart}
}

// build validates in, including that its category exists.
func (uc *UseCase) build(ctx context.Context, in entity.ScheduleInput, keepStart *time.Time) (entity.Schedule, error) {
	s, errs := in.Build(uc.rules(keepStart))

	if _, bad := errs["categoryId"]; !bad && s.CategoryID != nil {
		cats, err := uc.r.Categories.ByIDs(ctx, []string{*s.CategoryID})
		if err != nil {
			return entity.Schedule{}, fmt.Errorf("ScheduleUseCase - category: %w", err)
		}

		if _, ok := cats[*s.CategoryID]; !ok {
			errs["categoryId"] = entity.DetailInvalid
		}
	}

	if len(errs) > 0 {
		return entity.Schedule{}, entity.Invalid(errs)
	}

	return s, nil
}

// Create -.
func (uc *UseCase) Create(ctx context.Context, ownerID string, in entity.ScheduleInput) (entity.ScheduleView, error) {
	ch, err := uc.channel(ctx, ownerID)
	if err != nil {
		return entity.ScheduleView{}, err
	}

	s, err := uc.build(ctx, in, nil)
	if err != nil {
		return entity.ScheduleView{}, err
	}

	s.ChannelID = ch.ID

	out, err := uc.r.Schedules.Create(ctx, s, uc.cfg.MaxActive, uc.now())
	if errors.Is(err, entity.ErrScheduleLimit) {
		return entity.ScheduleView{}, entity.Reject(entity.ErrScheduleLimit, "limit", strconv.Itoa(uc.cfg.MaxActive))
	}

	if err != nil {
		return entity.ScheduleView{}, fmt.Errorf("ScheduleUseCase - Create: %w", err)
	}

	return uc.view(ctx, out)
}

// Update -.
func (uc *UseCase) Update(ctx context.Context, ownerID, id string, in entity.ScheduleInput) (entity.ScheduleView, error) {
	old, err := uc.owned(ctx, ownerID, id)
	if err != nil {
		return entity.ScheduleView{}, err
	}

	s, err := uc.build(ctx, in, &old.StartAt)
	if err != nil {
		return entity.ScheduleView{}, err
	}

	s.ID, s.ChannelID, s.ParentID, s.Exdates = old.ID, old.ChannelID, old.ParentID, old.Exdates

	out, err := uc.r.Schedules.Update(ctx, s, !old.SameTiming(s), uc.now())
	if err != nil {
		return entity.ScheduleView{}, fmt.Errorf("ScheduleUseCase - Update: %w", err)
	}

	return uc.view(ctx, out)
}

// Cancel -.
func (uc *UseCase) Cancel(ctx context.Context, ownerID, id string) error {
	s, err := uc.owned(ctx, ownerID, id)
	if err != nil {
		return err
	}

	if err = uc.r.Schedules.Cancel(ctx, s, uc.now()); err != nil {
		return fmt.Errorf("ScheduleUseCase - Cancel: %w", err)
	}

	return nil
}

// occurrence checks that at is a future occurrence of the caller's schedule that is not on air.
func (uc *UseCase) occurrence(ctx context.Context, ownerID, id string, at time.Time) (entity.Schedule, error) {
	s, err := uc.owned(ctx, ownerID, id)
	if err != nil {
		return entity.Schedule{}, err
	}

	if !s.HasOccurrence(at) {
		return entity.Schedule{}, entity.ErrNotFound
	}

	if !at.After(uc.now()) {
		return entity.Schedule{}, entity.ErrInvalidState
	}

	live, err := uc.r.Schedules.OccurrenceLive(ctx, s.ID, at)
	if err != nil {
		return entity.Schedule{}, fmt.Errorf("ScheduleUseCase - occurrence live: %w", err)
	}

	if live {
		return entity.Schedule{}, entity.ErrInvalidState
	}

	return s, nil
}

// MoveOccurrence turns one occurrence into a one-off at newStart (parentId = the series).
func (uc *UseCase) MoveOccurrence(ctx context.Context, ownerID, id string, at, newStart time.Time) (entity.ScheduleView, error) {
	s, err := uc.occurrence(ctx, ownerID, id, at)
	if err != nil {
		return entity.ScheduleView{}, err
	}

	moved, err := uc.build(ctx, entity.ScheduleInput{
		Title: s.Title, CategoryID: s.CategoryID, StartAt: newStart, DurationMin: s.DurationMin, Timezone: s.Timezone,
	}, nil)
	if err != nil {
		return entity.ScheduleView{}, err
	}

	moved.ChannelID, moved.ParentID = s.ChannelID, &s.ID

	out, err := uc.r.Schedules.MoveOccurrence(ctx, s, at, moved)
	if err != nil {
		return entity.ScheduleView{}, fmt.Errorf("ScheduleUseCase - MoveOccurrence: %w", err)
	}

	return uc.view(ctx, out)
}

// CancelOccurrence -.
func (uc *UseCase) CancelOccurrence(ctx context.Context, ownerID, id string, at time.Time) error {
	s, err := uc.occurrence(ctx, ownerID, id, at)
	if err != nil {
		return err
	}

	if err = uc.r.Schedules.CancelOccurrence(ctx, s, at); err != nil {
		return fmt.Errorf("ScheduleUseCase - CancelOccurrence: %w", err)
	}

	return nil
}

func (uc *UseCase) view(ctx context.Context, s entity.Schedule) (entity.ScheduleView, error) {
	views, err := uc.views(ctx, []entity.Schedule{s})
	if err != nil {
		return entity.ScheduleView{}, err
	}

	return views[0], nil
}

// views adds the category and the next occurrences (with reminder counts and SCHEDULED stream ids).
func (uc *UseCase) views(ctx context.Context, list []entity.Schedule) ([]entity.ScheduleView, error) {
	now := uc.now()
	ids := make([]string, 0, len(list))
	catIDs := []string{}

	for _, s := range list {
		ids = append(ids, s.ID)

		if s.CategoryID != nil {
			catIDs = append(catIDs, *s.CategoryID)
		}
	}

	counts, err := uc.r.Reminders.Counts(ctx, ids, now)
	if err != nil {
		return nil, fmt.Errorf("ScheduleUseCase - reminder counts: %w", err)
	}

	streams, err := uc.r.Schedules.OccurrenceStreams(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("ScheduleUseCase - occurrence streams: %w", err)
	}

	cats := uc.enrich.Categories(ctx, catIDs)
	out := make([]entity.ScheduleView, 0, len(list))

	for _, s := range list {
		v := entity.ScheduleView{Schedule: s, Next: []entity.NextOccurrence{}}

		if s.CategoryID != nil {
			if c, ok := cats[*s.CategoryID]; ok {
				v.Category = &c
			}
		}

		if s.Status == entity.ScheduleActive {
			v.Next = next(s, s.Occurrences(now, now.Add(uc.cfg.Horizon), entity.NextOccurrencesMax), counts, streams)
		}

		out = append(out, v)
	}

	return out, nil
}

func next(s entity.Schedule, ats []time.Time, counts map[entity.OccurrenceKey]int, streams map[entity.OccurrenceKey]string) []entity.NextOccurrence {
	out := make([]entity.NextOccurrence, 0, len(ats))

	for _, at := range ats {
		n := entity.NextOccurrence{At: at, ReminderCount: counts[entity.KeyOf(s.ID, at)]}
		if id, ok := streams[entity.KeyOf(s.ID, at)]; ok {
			n.StreamID = &id
		}

		out = append(out, n)
	}

	return out
}

// ---- Reminders ------------------------------------------------------------------------------------

// Remind -.
func (uc *UseCase) Remind(ctx context.Context, userID, scheduleID string, at time.Time) (entity.Reminder, bool, error) {
	s, err := uc.r.Schedules.GetPublic(ctx, scheduleID)
	if err != nil {
		return entity.Reminder{}, false, fmt.Errorf("ScheduleUseCase - Remind: %w", err)
	}

	if s.Status != entity.ScheduleActive {
		return entity.Reminder{}, false, entity.ErrInvalidState
	}

	if !s.HasOccurrence(at) {
		return entity.Reminder{}, false, entity.ErrNotFound
	}

	if !at.After(uc.now()) {
		return entity.Reminder{}, false, entity.ErrInvalidState
	}

	r, created, err := uc.r.Reminders.Add(ctx, entity.Reminder{UserID: userID, ScheduleID: s.ID, OccurrenceAt: at.UTC()})
	if err != nil {
		return entity.Reminder{}, false, fmt.Errorf("ScheduleUseCase - Remind: %w", err)
	}

	return r, created, nil
}

// Unremind -.
func (uc *UseCase) Unremind(ctx context.Context, userID, scheduleID string, at time.Time) error {
	if err := uc.r.Reminders.Remove(ctx, userID, scheduleID, at.UTC()); err != nil {
		return fmt.Errorf("ScheduleUseCase - Unremind: %w", err)
	}

	return nil
}

// ---- Jobs -----------------------------------------------------------------------------------------

// AnnounceUpcoming announces each occurrence in (watermark or now, now + lead] exactly once per schedule
// version; stream-svc creates one SCHEDULED stream per occurrence (a repeated announcement is a no-op there).
func (uc *UseCase) AnnounceUpcoming(ctx context.Context) (int, error) {
	now := uc.now()
	until := now.Add(uc.cfg.UpcomingLead)

	n, err := uc.r.Schedules.AnnounceDue(ctx, until, _announceBatch, func(s entity.Schedule) []events.ScheduleOccurrenceUpcoming {
		from := now
		if s.UpcomingNotifiedUntil != nil && s.UpcomingNotifiedUntil.After(from) {
			from = *s.UpcomingNotifiedUntil
		}

		out := []events.ScheduleOccurrenceUpcoming{}
		for _, at := range s.Occurrences(from.Add(_instant), until.Add(_instant), _announceMax) {
			out = append(out, events.ScheduleOccurrenceUpcoming{
				ScheduleID: s.ID, ChannelID: s.ChannelID, Title: s.Title, OccurrenceAt: at,
				CategoryID: s.CategoryID, DurationMin: s.DurationMin,
			})
		}

		return out
	})
	if err != nil {
		return 0, fmt.Errorf("ScheduleUseCase - AnnounceUpcoming: %w", err)
	}

	return n, nil
}

// DueReminders -.
func (uc *UseCase) DueReminders(ctx context.Context) (int, error) {
	n, err := uc.r.Reminders.MarkDue(ctx, uc.cfg.ReminderLead, _reminderBatch)
	if err != nil {
		return 0, fmt.Errorf("ScheduleUseCase - DueReminders: %w", err)
	}

	return n, nil
}
