package persistent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

var _ repo.ScheduleRepo = (*Repo)(nil)

const _scheduleCols = `id::text, channel_id::text, category_id::text, title, start_at, duration_min, rrule, timezone, status,
	exdates, parent_id::text, last_occurrence_at, upcoming_notified_until, created_at, updated_at`

func scanSchedule(row scanner) (entity.Schedule, error) {
	var (
		s      entity.Schedule
		rrule  *string
		status string
	)

	err := row.Scan(&s.ID, &s.ChannelID, &s.CategoryID, &s.Title, &s.StartAt, &s.DurationMin, &rrule, &s.Timezone, &status,
		&s.Exdates, &s.ParentID, &s.LastOccurrenceAt, &s.UpcomingNotifiedUntil, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Schedule{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Schedule{}, fmt.Errorf("scan schedule: %w", err)
	}

	s.Status = entity.ScheduleStatus(status)
	s.StartAt = s.StartAt.UTC()

	if rrule != nil {
		if r, ok := entity.ParseRRule(*rrule); ok {
			s.RRule = &r
		}
	}

	return s, nil
}

func rruleOf(s entity.Schedule) *string {
	if s.RRule == nil {
		return nil
	}

	v := s.RRule.String()

	return &v
}

func scanSchedules(rows pgx.Rows) ([]entity.Schedule, error) {
	defer rows.Close()

	out := []entity.Schedule{}

	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan schedules: %w", err)
	}

	return out, nil
}

func insertSchedule(ctx context.Context, tx pgx.Tx, s entity.Schedule) (entity.Schedule, error) {
	out, err := scanSchedule(tx.QueryRow(ctx,
		`INSERT INTO schedules (channel_id, category_id, title, start_at, duration_min, rrule, timezone, parent_id,
		                        last_occurrence_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING `+_scheduleCols,
		s.ChannelID, s.CategoryID, s.Title, s.StartAt, s.DurationMin, rruleOf(s), s.Timezone, s.ParentID, s.LastOccurrenceAt))
	if err != nil {
		return entity.Schedule{}, fmt.Errorf("Repo - insert schedule: %w", err)
	}

	return out, nil
}

// Create -.
func (r *Repo) Create(ctx context.Context, s entity.Schedule, maxActive int, now time.Time) (entity.Schedule, error) {
	var out entity.Schedule

	err := r.inTx(ctx, "Repo - Create schedule", func(tx pgx.Tx) error {
		// The channel row lock serializes concurrent creations, so the limit cannot be passed twice.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM channels WHERE id = $1 FOR UPDATE`, s.ChannelID); err != nil {
			return fmt.Errorf("Repo - Create schedule - lock: %w", err)
		}

		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM schedules
			 WHERE channel_id = $1 AND status = 'ACTIVE' AND (last_occurrence_at IS NULL OR last_occurrence_at > $2)`,
			s.ChannelID, now).Scan(&n); err != nil {
			return fmt.Errorf("Repo - Create schedule - count: %w", err)
		}

		if n >= maxActive {
			return entity.ErrScheduleLimit
		}

		var err error

		if out, err = insertSchedule(ctx, tx, s); err != nil {
			return err
		}

		return addOutbox(ctx, tx, "schedule", out.ID, events.TypeScheduleCreated, events.ScheduleCreated{
			ScheduleID: out.ID, ChannelID: out.ChannelID, Title: out.Title, StartAt: out.StartAt,
		})
	})

	return out, err
}

// Update -.
func (r *Repo) Update(ctx context.Context, s entity.Schedule, timing bool, now time.Time) (entity.Schedule, error) {
	var out entity.Schedule

	err := r.inTx(ctx, "Repo - Update schedule", func(tx pgx.Tx) error {
		var err error

		out, err = scanSchedule(tx.QueryRow(ctx,
			`UPDATE schedules SET category_id = $2, title = $3, start_at = $4, duration_min = $5, rrule = $6, timezone = $7,
			        last_occurrence_at = $8,
			        upcoming_notified_until = CASE WHEN $9 THEN NULL ELSE upcoming_notified_until END,
			        updated_at = now()
			 WHERE id = $1 AND status = 'ACTIVE'
			 RETURNING `+_scheduleCols,
			s.ID, s.CategoryID, s.Title, s.StartAt, s.DurationMin, rruleOf(s), s.Timezone, s.LastOccurrenceAt, timing))
		if errors.Is(err, entity.ErrNotFound) {
			return entity.ErrInvalidState
		}

		if err != nil {
			return fmt.Errorf("Repo - Update schedule: %w", err)
		}

		if timing {
			return cancelFuture(ctx, tx, out, now, events.OccurrenceSeriesChanged)
		}

		return addOutbox(ctx, tx, "schedule", out.ID, events.TypeScheduleUpdated, events.ScheduleUpdated{
			ScheduleID: out.ID, ChannelID: out.ChannelID, Title: out.Title, CategoryID: out.CategoryID,
			DurationMin: out.DurationMin,
		})
	})

	return out, err
}

// Cancel -.
func (r *Repo) Cancel(ctx context.Context, s entity.Schedule, now time.Time) error {
	return r.inTx(ctx, "Repo - Cancel schedule", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE schedules SET status = 'CANCELLED', updated_at = now() WHERE id = $1 AND status = 'ACTIVE'`, s.ID)
		if err != nil {
			return fmt.Errorf("Repo - Cancel schedule: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return entity.ErrInvalidState
		}

		return cancelFuture(ctx, tx, s, now, events.OccurrenceCancelled)
	})
}

// cancelFuture drops the reminders and stream links of s's occurrences after now and announces each
// occurrence that had one of them (stream-svc cancels its SCHEDULED stream).
func cancelFuture(ctx context.Context, tx pgx.Tx, s entity.Schedule, now time.Time, reason string) error {
	users, err := deleteReminders(ctx, tx, `schedule_id = $1 AND occurrence_at > $2`, s.ID, now)
	if err != nil {
		return err
	}

	rows, err := tx.Query(ctx,
		`DELETE FROM occurrence_streams WHERE schedule_id = $1 AND occurrence_at > $2 RETURNING occurrence_at`, s.ID, now)
	if err != nil {
		return fmt.Errorf("Repo - cancel future - streams: %w", err)
	}

	ats, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		return fmt.Errorf("Repo - cancel future - streams: %w", err)
	}

	for _, at := range ats {
		if _, ok := users[at.UnixMicro()]; !ok {
			users[at.UnixMicro()] = []string{}
		}
	}

	keys := make([]int64, 0, len(users))
	for k := range users {
		keys = append(keys, k)
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	for _, k := range keys {
		if err := addOutbox(ctx, tx, "schedule", s.ID, events.TypeScheduleOccurrenceChanged, events.ScheduleOccurrenceChanged{
			ScheduleID: s.ID, ChannelID: s.ChannelID, Title: s.Title, OccurrenceAt: time.UnixMicro(k).UTC(),
			Reason: reason, UserIDs: users[k],
		}); err != nil {
			return err
		}
	}

	return nil
}

// deleteReminders deletes the reminders matching where ($1 = schedule id, $2 = instant) and returns their
// users keyed by occurrence (UnixMicro).
func deleteReminders(ctx context.Context, tx pgx.Tx, where string, args ...any) (map[int64][]string, error) {
	rows, err := tx.Query(ctx, `DELETE FROM reminders WHERE `+where+` RETURNING user_id::text, occurrence_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - delete reminders: %w", err)
	}
	defer rows.Close()

	out := map[int64][]string{}

	for rows.Next() {
		var (
			user string
			at   time.Time
		)

		if err = rows.Scan(&user, &at); err != nil {
			return nil, fmt.Errorf("Repo - delete reminders - scan: %w", err)
		}

		out[at.UnixMicro()] = append(out[at.UnixMicro()], user)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - delete reminders: %w", err)
	}

	return out, nil
}

func excludeOccurrence(ctx context.Context, tx pgx.Tx, id string, at time.Time) error {
	tag, err := tx.Exec(ctx,
		`UPDATE schedules SET exdates = array_append(exdates, $2), updated_at = now()
		 WHERE id = $1 AND status = 'ACTIVE' AND NOT ($2 = ANY(exdates))`, id, at)
	if err != nil {
		return fmt.Errorf("Repo - exclude occurrence: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return entity.ErrInvalidState
	}

	return nil
}

// CancelOccurrence -.
func (r *Repo) CancelOccurrence(ctx context.Context, s entity.Schedule, at time.Time) error {
	return r.inTx(ctx, "Repo - CancelOccurrence", func(tx pgx.Tx) error {
		if err := excludeOccurrence(ctx, tx, s.ID, at); err != nil {
			return err
		}

		users, err := deleteReminders(ctx, tx, `schedule_id = $1 AND occurrence_at = $2`, s.ID, at)
		if err != nil {
			return err
		}

		if _, err = tx.Exec(ctx, `DELETE FROM occurrence_streams WHERE schedule_id = $1 AND occurrence_at = $2`, s.ID, at); err != nil {
			return fmt.Errorf("Repo - CancelOccurrence - stream: %w", err)
		}

		userIDs := users[at.UnixMicro()]
		if userIDs == nil {
			userIDs = []string{}
		}

		return addOutbox(ctx, tx, "schedule", s.ID, events.TypeScheduleOccurrenceChanged, events.ScheduleOccurrenceChanged{
			ScheduleID: s.ID, ChannelID: s.ChannelID, Title: s.Title, OccurrenceAt: at.UTC(),
			Reason: events.OccurrenceCancelled, UserIDs: userIDs,
		})
	})
}

// MoveOccurrence -.
func (r *Repo) MoveOccurrence(ctx context.Context, s entity.Schedule, at time.Time, moved entity.Schedule) (entity.Schedule, error) {
	var out entity.Schedule

	err := r.inTx(ctx, "Repo - MoveOccurrence", func(tx pgx.Tx) error {
		if err := excludeOccurrence(ctx, tx, s.ID, at); err != nil {
			return err
		}

		var err error

		if out, err = insertSchedule(ctx, tx, moved); err != nil {
			return err
		}

		rows, err := tx.Query(ctx,
			`UPDATE reminders SET schedule_id = $3, occurrence_at = $4, notified_at = NULL
			 WHERE schedule_id = $1 AND occurrence_at = $2 RETURNING user_id::text`, s.ID, at, out.ID, out.StartAt)
		if err != nil {
			return fmt.Errorf("Repo - MoveOccurrence - reminders: %w", err)
		}

		users, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return fmt.Errorf("Repo - MoveOccurrence - reminders: %w", err)
		}

		if _, err = tx.Exec(ctx,
			`UPDATE occurrence_streams SET schedule_id = $3, occurrence_at = $4 WHERE schedule_id = $1 AND occurrence_at = $2`,
			s.ID, at, out.ID, out.StartAt); err != nil {
			return fmt.Errorf("Repo - MoveOccurrence - stream: %w", err)
		}

		newAt := out.StartAt

		return addOutbox(ctx, tx, "schedule", s.ID, events.TypeScheduleOccurrenceChanged, events.ScheduleOccurrenceChanged{
			ScheduleID: s.ID, ChannelID: s.ChannelID, Title: s.Title, OccurrenceAt: at.UTC(), NewScheduleID: &out.ID,
			NewOccurrenceAt: &newAt, Reason: events.OccurrenceMoved, UserIDs: users,
		})
	})

	return out, err
}

// Get -.
func (r *Repo) Get(ctx context.Context, channelID, id string) (entity.Schedule, error) {
	return r.scheduleWhere(ctx, "id = $1 AND channel_id = $2", id, channelID)
}

// GetPublic -.
func (r *Repo) GetPublic(ctx context.Context, id string) (entity.Schedule, error) {
	return r.scheduleWhere(ctx, "id = $1", id)
}

func (r *Repo) scheduleWhere(ctx context.Context, where string, args ...any) (entity.Schedule, error) {
	s, err := scanSchedule(r.Pool.QueryRow(ctx, `SELECT `+_scheduleCols+` FROM schedules WHERE `+where, args...))
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return entity.Schedule{}, fmt.Errorf("Repo - Get schedule: %w", err)
	}

	return s, err
}

// ListByChannel lists ACTIVE schedules that still have an occurrence to come (or one under way), or the
// CANCELLED ones, oldest start first.
func (r *Repo) ListByChannel(ctx context.Context, channelID string, st entity.ScheduleStatus) ([]entity.Schedule, error) {
	q := `SELECT ` + _scheduleCols + ` FROM schedules WHERE channel_id = $1 AND status = $2`
	if st == entity.ScheduleActive {
		q += ` AND (last_occurrence_at IS NULL OR last_occurrence_at > now() - make_interval(mins => duration_min))`
	}

	rows, err := r.Pool.Query(ctx, q+` ORDER BY start_at, id`, channelID, string(st))
	if err != nil {
		return nil, fmt.Errorf("Repo - ListByChannel: %w", err)
	}

	out, err := scanSchedules(rows)
	if err != nil {
		return nil, fmt.Errorf("Repo - ListByChannel: %w", err)
	}

	return out, nil
}

// OccurrenceStreams -.
func (r *Repo) OccurrenceStreams(ctx context.Context, scheduleIDs []string) (map[entity.OccurrenceKey]string, error) {
	out := map[entity.OccurrenceKey]string{}
	if len(scheduleIDs) == 0 {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx,
		`SELECT schedule_id::text, occurrence_at, stream_id::text FROM occurrence_streams WHERE schedule_id = ANY($1::uuid[])`,
		scheduleIDs)
	if err != nil {
		return nil, fmt.Errorf("Repo - OccurrenceStreams: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, stream string
			at         time.Time
		)

		if err = rows.Scan(&id, &at, &stream); err != nil {
			return nil, fmt.Errorf("Repo - OccurrenceStreams - scan: %w", err)
		}

		out[entity.KeyOf(id, at)] = stream
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - OccurrenceStreams: %w", err)
	}

	return out, nil
}

// OccurrenceLive -.
func (r *Repo) OccurrenceLive(ctx context.Context, scheduleID string, at time.Time) (bool, error) {
	var live bool

	if err := r.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM occurrence_streams o JOIN live_streams l ON l.stream_id = o.stream_id
		                WHERE o.schedule_id = $1 AND o.occurrence_at = $2)`, scheduleID, at).Scan(&live); err != nil {
		return false, fmt.Errorf("Repo - OccurrenceLive: %w", err)
	}

	return live, nil
}

// UpsertOccurrenceStream -.
func (r *Repo) UpsertOccurrenceStream(ctx context.Context, eventID string, e events.StreamScheduled) error {
	return r.inEventTx(ctx, "Repo - UpsertOccurrenceStream", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM occurrence_streams WHERE stream_id = $3 AND (schedule_id, occurrence_at) <> ($1, $2)`,
			e.ScheduleID, e.ScheduledStartAt, e.StreamID); err != nil {
			return fmt.Errorf("Repo - UpsertOccurrenceStream - old: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO occurrence_streams (schedule_id, occurrence_at, stream_id)
			 SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM schedules WHERE id = $1)
			 ON CONFLICT (schedule_id, occurrence_at) DO UPDATE SET stream_id = EXCLUDED.stream_id`,
			e.ScheduleID, e.ScheduledStartAt, e.StreamID); err != nil {
			return fmt.Errorf("Repo - UpsertOccurrenceStream: %w", err)
		}

		return nil
	})
}

// AnnounceDue -.
func (r *Repo) AnnounceDue(ctx context.Context, until time.Time, batch int,
	fn func(entity.Schedule) []events.ScheduleOccurrenceUpcoming,
) (int, error) {
	var n int

	err := r.inTx(ctx, "Repo - AnnounceDue", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+_scheduleCols+` FROM schedules
			 WHERE status = 'ACTIVE' AND (upcoming_notified_until IS NULL OR upcoming_notified_until < $1)
			   AND (last_occurrence_at IS NULL OR last_occurrence_at > now())
			 ORDER BY upcoming_notified_until NULLS FIRST, id
			 LIMIT $2 FOR UPDATE SKIP LOCKED`, until, batch)
		if err != nil {
			return fmt.Errorf("Repo - AnnounceDue - select: %w", err)
		}

		due, err := scanSchedules(rows)
		if err != nil {
			return fmt.Errorf("Repo - AnnounceDue: %w", err)
		}

		ids := make([]string, 0, len(due))

		for _, s := range due {
			ids = append(ids, s.ID)

			for _, e := range fn(s) {
				if err := addOutbox(ctx, tx, "schedule", s.ID, events.TypeScheduleOccurrenceUpcoming, e); err != nil {
					return err
				}

				n++
			}
		}

		if _, err = tx.Exec(ctx,
			`UPDATE schedules SET upcoming_notified_until = $2 WHERE id = ANY($1::uuid[])`, ids, until); err != nil {
			return fmt.Errorf("Repo - AnnounceDue - watermark: %w", err)
		}

		return nil
	})

	return n, err
}
