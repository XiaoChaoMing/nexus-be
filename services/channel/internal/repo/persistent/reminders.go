package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

var _ repo.ReminderRepo = (*Repo)(nil)

// _reminderDueBatch is the most user ids one schedule.reminder_due event carries.
const _reminderDueBatch = 500

// Add -.
func (r *Repo) Add(ctx context.Context, rem entity.Reminder) (entity.Reminder, bool, error) {
	out := rem

	err := r.Pool.QueryRow(ctx,
		`INSERT INTO reminders (user_id, schedule_id, occurrence_at) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING RETURNING created_at`, rem.UserID, rem.ScheduleID, rem.OccurrenceAt).Scan(&out.CreatedAt)
	if err == nil {
		return out, true, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return entity.Reminder{}, false, fmt.Errorf("Repo - Add reminder: %w", err)
	}

	if err = r.Pool.QueryRow(ctx,
		`SELECT created_at FROM reminders WHERE user_id = $1 AND schedule_id = $2 AND occurrence_at = $3`,
		rem.UserID, rem.ScheduleID, rem.OccurrenceAt).Scan(&out.CreatedAt); err != nil {
		return entity.Reminder{}, false, fmt.Errorf("Repo - Add reminder - existing: %w", err)
	}

	return out, false, nil
}

// Remove -.
func (r *Repo) Remove(ctx context.Context, userID, scheduleID string, at time.Time) error {
	if _, err := r.Pool.Exec(ctx,
		`DELETE FROM reminders WHERE user_id = $1 AND schedule_id = $2 AND occurrence_at = $3`, userID, scheduleID, at); err != nil {
		return fmt.Errorf("Repo - Remove reminder: %w", err)
	}

	return nil
}

// Reminded -.
func (r *Repo) Reminded(ctx context.Context, userID string, scheduleIDs []string) (map[entity.OccurrenceKey]bool, error) {
	out := map[entity.OccurrenceKey]bool{}

	counts, err := r.reminderKeys(ctx, "Reminded",
		`SELECT schedule_id::text, occurrence_at, 1 FROM reminders WHERE user_id = $1 AND schedule_id = ANY($2::uuid[])`,
		userID, scheduleIDs)
	for k := range counts {
		out[k] = true
	}

	return out, err
}

// Counts -.
func (r *Repo) Counts(ctx context.Context, scheduleIDs []string, from time.Time) (map[entity.OccurrenceKey]int, error) {
	return r.reminderKeys(ctx, "Counts",
		`SELECT schedule_id::text, occurrence_at, count(*)::int FROM reminders
		 WHERE schedule_id = ANY($1::uuid[]) AND occurrence_at >= $2 GROUP BY schedule_id, occurrence_at`,
		scheduleIDs, from)
}

func (r *Repo) reminderKeys(ctx context.Context, op, q string, args ...any) (map[entity.OccurrenceKey]int, error) {
	out := map[entity.OccurrenceKey]int{}

	rows, err := r.Pool.Query(ctx, q, args...)
	if err != nil {
		return out, fmt.Errorf("Repo - %s: %w", op, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id string
			at time.Time
			n  int
		)

		if err = rows.Scan(&id, &at, &n); err != nil {
			return out, fmt.Errorf("Repo - %s - scan: %w", op, err)
		}

		out[entity.KeyOf(id, at)] = n
	}

	if err = rows.Err(); err != nil {
		return out, fmt.Errorf("Repo - %s: %w", op, err)
	}

	return out, nil
}

type dueReminder struct {
	scheduleID, channelID, title string
	at                           time.Time
	users                        []string
}

// MarkDue -.
func (r *Repo) MarkDue(ctx context.Context, lead time.Duration, batch int) (int, error) {
	var n int

	err := r.inTx(ctx, "Repo - MarkDue", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`WITH due AS (
				SELECT user_id, schedule_id, occurrence_at FROM reminders
				WHERE notified_at IS NULL AND occurrence_at <= now() + make_interval(secs => $1)
				  AND occurrence_at > now() - interval '5 minutes'
				ORDER BY occurrence_at LIMIT $2 FOR UPDATE SKIP LOCKED
			 ), marked AS (
				UPDATE reminders r SET notified_at = now() FROM due
				WHERE (r.user_id, r.schedule_id, r.occurrence_at) = (due.user_id, due.schedule_id, due.occurrence_at)
				RETURNING r.user_id, r.schedule_id, r.occurrence_at
			 )
			 SELECT m.schedule_id::text, s.channel_id::text, s.title, m.occurrence_at, array_agg(m.user_id::text ORDER BY m.user_id)
			 FROM marked m JOIN schedules s ON s.id = m.schedule_id
			 -- Reminders of a suspended channel are consumed without a notification.
			 JOIN channels c ON c.id = s.channel_id AND c.status = 'ACTIVE'
			 GROUP BY m.schedule_id, s.channel_id, s.title, m.occurrence_at`, lead.Seconds(), batch)
		if err != nil {
			return fmt.Errorf("Repo - MarkDue: %w", err)
		}

		due, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (dueReminder, error) {
			var d dueReminder

			return d, row.Scan(&d.scheduleID, &d.channelID, &d.title, &d.at, &d.users)
		})
		if err != nil {
			return fmt.Errorf("Repo - MarkDue - scan: %w", err)
		}

		for _, d := range due {
			n += len(d.users)

			if err := queueReminderDue(ctx, tx, d); err != nil {
				return err
			}
		}

		return nil
	})

	return n, err
}

func queueReminderDue(ctx context.Context, tx pgx.Tx, d dueReminder) error {
	for start := 0; start < len(d.users); start += _reminderDueBatch {
		end := min(start+_reminderDueBatch, len(d.users))

		if err := addOutbox(ctx, tx, "schedule", d.scheduleID, events.TypeScheduleReminderDue, events.ScheduleReminderDue{
			ScheduleID: d.scheduleID, ChannelID: d.channelID, Title: d.title, OccurrenceAt: d.at.UTC(),
			UserIDs: d.users[start:end],
		}); err != nil {
			return err
		}
	}

	return nil
}
