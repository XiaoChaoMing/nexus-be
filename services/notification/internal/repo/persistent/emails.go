package persistent

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
)

// Due -.
func (r *Repo) Due(ctx context.Context, limit int, lease time.Duration) ([]entity.OutboxEmail, error) {
	rows, err := r.Pool.Query(ctx, `UPDATE email_outbox SET next_attempt_at = now() + $2::interval
		WHERE id IN (SELECT id FROM email_outbox WHERE status = 'PENDING' AND next_attempt_at <= now()
			ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING id::text, to_email::text, template, locale, data, attempts`,
		limit, strconv.FormatInt(lease.Microseconds(), 10)+" microseconds")
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - Due: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.OutboxEmail, error) {
		var e entity.OutboxEmail

		err := row.Scan(&e.ID, &e.To, &e.Template, &e.Locale, &e.Data, &e.Attempts)

		return e, err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("EmailRepo - Due - scan: %w", err)
	}

	return out, nil
}

// MarkSent -.
func (r *Repo) MarkSent(ctx context.Context, id string) error {
	if _, err := r.Pool.Exec(ctx, `UPDATE email_outbox SET status = 'SENT', sent_at = now(), attempts = attempts + 1,
		last_error = NULL WHERE id = $1`, id); err != nil {
		return fmt.Errorf("EmailRepo - MarkSent: %w", err)
	}

	return nil
}

// MarkFailed -.
func (r *Repo) MarkFailed(ctx context.Context, id string, attempts int, next *time.Time, errText string) error {
	_, err := r.Pool.Exec(ctx, `UPDATE email_outbox SET attempts = $2, last_error = $3,
		status = CASE WHEN $4::timestamptz IS NULL THEN 'FAILED' ELSE 'PENDING' END,
		next_attempt_at = coalesce($4, next_attempt_at) WHERE id = $1`, id, attempts, errText, next)
	if err != nil {
		return fmt.Errorf("EmailRepo - MarkFailed: %w", err)
	}

	return nil
}

// PurgeDone deletes SENT and FAILED rows created before before.
func (r *Repo) PurgeDone(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `DELETE FROM email_outbox WHERE status IN ('SENT', 'FAILED') AND created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("EmailRepo - PurgeDone: %w", err)
	}

	return tag.RowsAffected(), nil
}
