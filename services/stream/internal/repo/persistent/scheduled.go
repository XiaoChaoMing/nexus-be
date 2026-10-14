package persistent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
)

// ListOnAirFiltered -.
func (r *Repo) ListOnAirFiltered(ctx context.Context, f repo.StreamFilter, after *repo.Cursor, limit int) ([]entity.Stream, error) {
	w := onAirWhere(f)

	if after != nil {
		w.add("(s.started_at, s.id) < (?, ?::uuid)", after.StartedAt, after.ID)
	}

	w.args = append(w.args, limit)

	return r.streams(ctx, "ListOnAirFiltered",
		`SELECT `+_streamCols+_streamFrom+`WHERE `+w.sql()+` ORDER BY s.started_at DESC, s.id DESC LIMIT $`+strconv.Itoa(len(w.args)),
		w.args...)
}

// ListOnAirAll -.
func (r *Repo) ListOnAirAll(ctx context.Context, f repo.StreamFilter, maxRows int) ([]entity.Stream, error) {
	w := onAirWhere(f)
	w.args = append(w.args, maxRows)

	return r.streams(ctx, "ListOnAirAll",
		`SELECT `+_streamCols+_streamFrom+`WHERE `+w.sql()+` LIMIT $`+strconv.Itoa(len(w.args)), w.args...)
}

func onAirWhere(f repo.StreamFilter) *where {
	w := &where{}
	w.add("s.status IN ('LIVE', 'INTERRUPTED')")

	if len(f.CategoryIDs) > 0 {
		w.add("s.category_id = ANY(?::uuid[])", f.CategoryIDs)
	}

	if f.Language != "" {
		w.add("s.language = ?", f.Language)
	}

	if len(f.Tags) > 0 {
		w.add("s.tags @> ?::text[]", f.Tags)
	}

	return w
}

func (r *Repo) streams(ctx context.Context, op, q string, args ...any) ([]entity.Stream, error) {
	rows, err := r.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}
	defer rows.Close()

	out := []entity.Stream{}

	for rows.Next() {
		s, serr := scanStream(rows)
		if serr != nil {
			return nil, fmt.Errorf("Repo - %s: %w", op, serr)
		}

		out = append(out, s)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}

	return out, nil
}

// CreateScheduled inserts the SCHEDULED stream of an occurrence once (unique (schedule_id, scheduled_start_at)).
func (r *Repo) CreateScheduled(ctx context.Context, eventID string, e events.ScheduleOccurrenceUpcoming) error {
	return r.inEventTx(ctx, "Repo - CreateScheduled", eventID, func(tx pgx.Tx) error {
		var id string

		err := tx.QueryRow(ctx,
			`INSERT INTO streams (channel_id, schedule_id, title, category_id, is_mature, language, status, scheduled_start_at)
			 SELECT $1, $2, $3, $4, COALESCE(st.is_mature, false), COALESCE(st.language, 'vi'), 'SCHEDULED', $5
			 FROM (SELECT 1) one LEFT JOIN stream_settings st ON st.channel_id = $1
			 ON CONFLICT (schedule_id, scheduled_start_at) WHERE schedule_id IS NOT NULL AND status <> 'CANCELLED' DO NOTHING
			 RETURNING id::text`,
			e.ChannelID, e.ScheduleID, e.Title, e.CategoryID, e.OccurrenceAt).Scan(&id)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil // already created
		case err != nil:
			return fmt.Errorf("Repo - CreateScheduled: %w", err)
		}

		return addOutbox(ctx, tx, id, events.TypeStreamScheduled, events.StreamScheduled{
			StreamID: id, ChannelID: e.ChannelID, ScheduleID: e.ScheduleID, ScheduledStartAt: e.OccurrenceAt.UTC(),
		})
	})
}

// ChangeScheduled cancels or moves the SCHEDULED stream of an occurrence; nothing happens without one.
func (r *Repo) ChangeScheduled(ctx context.Context, eventID string, e events.ScheduleOccurrenceChanged) error {
	return r.inEventTx(ctx, "Repo - ChangeScheduled", eventID, func(tx pgx.Tx) error {
		if e.Reason == events.OccurrenceMoved && e.NewScheduleID != nil && e.NewOccurrenceAt != nil {
			return moveScheduled(ctx, tx, e)
		}

		_, err := cancelScheduled(ctx, tx, `schedule_id = $1 AND scheduled_start_at = $2`, e.ScheduleID, e.OccurrenceAt)

		return err
	})
}

func moveScheduled(ctx context.Context, tx pgx.Tx, e events.ScheduleOccurrenceChanged) error {
	var id, channelID string

	err := tx.QueryRow(ctx,
		`UPDATE streams SET schedule_id = $3, scheduled_start_at = $4
		 WHERE schedule_id = $1 AND scheduled_start_at = $2 AND status = 'SCHEDULED'
		 RETURNING id::text, channel_id::text`,
		e.ScheduleID, e.OccurrenceAt, *e.NewScheduleID, *e.NewOccurrenceAt).Scan(&id, &channelID)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("Repo - ChangeScheduled - move: %w", err)
	}

	at := e.NewOccurrenceAt.UTC()

	if err := addOutbox(ctx, tx, id, events.TypeStreamScheduled, events.StreamScheduled{
		StreamID: id, ChannelID: channelID, ScheduleID: *e.NewScheduleID, ScheduledStartAt: at,
	}); err != nil {
		return err
	}

	return addOutbox(ctx, tx, id, events.TypeStreamStatusChanged, events.StreamStatusChanged{
		StreamID: id, ChannelID: channelID, Status: events.StreamStatusScheduled, OccurredAt: time.Now().UTC(), ScheduledStartAt: &at,
	})
}

// cancelScheduled moves the SCHEDULED streams matching where to CANCELLED, announces each one and returns how many.
func cancelScheduled(ctx context.Context, tx pgx.Tx, where string, args ...any) (int, error) {
	rows, err := tx.Query(ctx,
		`UPDATE streams SET status = 'CANCELLED' WHERE id IN (
			SELECT id FROM streams WHERE status = 'SCHEDULED' AND `+where+` FOR UPDATE SKIP LOCKED
		 ) RETURNING id::text, channel_id::text, now()`, args...)
	if err != nil {
		return 0, fmt.Errorf("Repo - cancel scheduled: %w", err)
	}

	type cancelled struct {
		id, channelID string
		at            time.Time
	}

	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (cancelled, error) {
		var c cancelled

		return c, row.Scan(&c.id, &c.channelID, &c.at)
	})
	if err != nil {
		return 0, fmt.Errorf("Repo - cancel scheduled - scan: %w", err)
	}

	for _, c := range list {
		if err := addOutbox(ctx, tx, c.id, events.TypeStreamStatusChanged, events.StreamStatusChanged{
			StreamID: c.id, ChannelID: c.channelID, Status: events.StreamStatusCancelled, OccurredAt: c.at.UTC(),
		}); err != nil {
			return 0, err
		}
	}

	return len(list), nil
}

// RetitleScheduled -.
func (r *Repo) RetitleScheduled(ctx context.Context, eventID string, e events.ScheduleUpdated) error {
	return r.inEventTx(ctx, "Repo - RetitleScheduled", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE streams SET title = $2, category_id = $3 WHERE schedule_id = $1 AND status = 'SCHEDULED'`,
			e.ScheduleID, e.Title, e.CategoryID); err != nil {
			return fmt.Errorf("Repo - RetitleScheduled: %w", err)
		}

		return nil
	})
}

// CancelLateScheduled -.
func (r *Repo) CancelLateScheduled(ctx context.Context, late time.Duration, batch int) (int, error) {
	n := 0

	err := r.inTx(ctx, "Repo - CancelLateScheduled", func(tx pgx.Tx) error {
		var ids []string

		rows, err := tx.Query(ctx,
			`SELECT id::text FROM streams WHERE status = 'SCHEDULED' AND scheduled_start_at < now() - make_interval(secs => $1)
			 ORDER BY scheduled_start_at LIMIT $2`, late.Seconds(), batch)
		if err != nil {
			return fmt.Errorf("Repo - CancelLateScheduled - select: %w", err)
		}

		if ids, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return fmt.Errorf("Repo - CancelLateScheduled - scan: %w", err)
		}

		n, err = cancelScheduled(ctx, tx, `id = ANY($1::uuid[])`, ids)

		return err
	})

	return n, err
}
