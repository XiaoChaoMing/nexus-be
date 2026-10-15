package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
)

const _cols = `id::text, user_id::text, type, payload, link, group_key, group_count, read_at, created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanNotification(row scanner) (entity.Notification, error) {
	var (
		n   entity.Notification
		typ string
	)

	err := row.Scan(&n.ID, &n.UserID, &typ, &n.Payload, &n.Link, &n.GroupKey, &n.GroupCount, &n.ReadAt, &n.CreatedAt,
		&n.UpdatedAt)
	n.Type = entity.Type(typ)

	return n, err //nolint:wrapcheck // callers wrap
}

func payloadOf(n entity.Notification) json.RawMessage {
	if len(n.Payload) == 0 {
		return json.RawMessage(`{}`)
	}

	return n.Payload
}

// InsertBatch -.
func (r *Repo) InsertBatch(ctx context.Context, eventID string, n entity.Notification, userIDs []string,
	emails []entity.Email,
) (repo.BatchResult, error) {
	var res repo.BatchResult

	err := r.inTx(ctx, "NotificationRepo - InsertBatch", func(tx pgx.Tx) error {
		inserted, err := insertMany(ctx, tx, eventID, n, userIDs)
		if err != nil {
			return err
		}

		for _, row := range inserted {
			res.Notified = append(res.Notified, row.UserID)
		}

		if res.Emailed, err = insertEmails(ctx, tx, eventID, emails); err != nil {
			return err
		}

		return announce(ctx, tx, inserted)
	})
	if err != nil {
		return repo.BatchResult{}, fmt.Errorf("NotificationRepo - InsertBatch: %w", err)
	}

	return res, nil
}

func insertMany(ctx context.Context, tx pgx.Tx, eventID string, n entity.Notification, userIDs []string,
) ([]entity.Notification, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	rows, err := tx.Query(ctx, `INSERT INTO notifications (user_id, type, payload, link, group_key, source_event_id)
		SELECT u, $2, $3, $4, $5, $6 FROM unnest($1::uuid[]) AS u
		ON CONFLICT (user_id, source_event_id) DO NOTHING
		RETURNING `+_cols, userIDs, string(n.Type), payloadOf(n), n.Link, n.GroupKey, eventID)
	if err != nil {
		return nil, fmt.Errorf("insert notifications: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.Notification, error) {
		return scanNotification(row)
	})
	if err != nil {
		return nil, fmt.Errorf("insert notifications - scan: %w", err)
	}

	return out, nil
}

// insertEmails queues the e-mails of one event (same template), linked to the event's notification of each
// user; one row per (user, event) survives redeliveries.
func insertEmails(ctx context.Context, tx pgx.Tx, eventID string, emails []entity.Email) ([]string, error) {
	if len(emails) == 0 {
		return nil, nil
	}

	users := make([]string, 0, len(emails))
	tos := make([]string, 0, len(emails))
	data := make([]string, 0, len(emails))
	locales := make([]string, 0, len(emails))

	for _, e := range emails {
		b, err := json.Marshal(e.Data)
		if err != nil {
			return nil, fmt.Errorf("email data: %w", err)
		}

		users, tos, data, locales = append(users, e.UserID), append(tos, e.To), append(data, string(b)), append(locales, e.Locale)
	}

	rows, err := tx.Query(ctx, `INSERT INTO email_outbox
		(user_id, to_email, data, locale, template, mandatory, source_event_id, notification_id)
		SELECT e.u, e.t, e.d::jsonb, e.l, $5, $6, $7,
			(SELECT n.id FROM notifications n WHERE n.user_id = e.u AND n.source_event_id = $7)
		FROM unnest($1::uuid[], $2::text[], $3::text[], $4::text[]) AS e(u, t, d, l)
		ON CONFLICT (user_id, source_event_id) WHERE user_id IS NOT NULL AND source_event_id IS NOT NULL DO NOTHING
		RETURNING user_id::text`, users, tos, data, locales, emails[0].Template, emails[0].Mandatory, eventID)
	if err != nil {
		return nil, fmt.Errorf("insert emails: %w", err)
	}

	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("insert emails - scan: %w", err)
	}

	return out, nil
}

// Upsert -.
func (r *Repo) Upsert(ctx context.Context, eventID string, items []repo.Pending) ([]repo.Upserted, error) {
	var out []repo.Upserted

	err := r.inTx(ctx, "NotificationRepo - Upsert", func(tx pgx.Tx) error {
		fresh, err := markProcessed(ctx, tx, eventID)
		if err != nil || !fresh {
			return err
		}

		changed := make([]entity.Notification, 0, len(items))

		for _, it := range items {
			u, err := upsertOne(ctx, tx, eventID, it)
			if err != nil {
				return err
			}

			if u.Notification.ID != "" {
				changed = append(changed, u.Notification)
			}

			out = append(out, u)
		}

		return announce(ctx, tx, changed)
	})
	if err != nil {
		return nil, fmt.Errorf("NotificationRepo - Upsert: %w", err)
	}

	return out, nil
}

func upsertOne(ctx context.Context, tx pgx.Tx, eventID string, it repo.Pending) (repo.Upserted, error) {
	n := it.Notification
	u := repo.Upserted{Notification: entity.Notification{UserID: n.UserID}}

	if !it.EmailOnly {
		row, grouped, err := groupOrInsert(ctx, tx, eventID, it)
		if err != nil {
			return u, err
		}

		u.Notification, u.Grouped = row, grouped
		if grouped || row.ID == "" {
			return u, nil
		}
	}

	if it.Email == nil {
		return u, nil
	}

	emailed, err := insertEmails(ctx, tx, eventID, []entity.Email{*it.Email})
	if err != nil {
		return u, err
	}

	u.Emailed = len(emailed) == 1

	return u, nil
}

// groupOrInsert updates the newest row of the same group key inside the window (BR-E12) or inserts one. A
// transaction-scoped advisory lock on (user, group key) serializes two events racing for the same group.
func groupOrInsert(ctx context.Context, tx pgx.Tx, eventID string, it repo.Pending) (entity.Notification, bool, error) {
	n := it.Notification

	if n.GroupKey != nil && it.Window > 0 {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			n.UserID+":"+*n.GroupKey); err != nil {
			return n, false, fmt.Errorf("group lock: %w", err)
		}

		row, err := scanNotification(tx.QueryRow(ctx, `UPDATE notifications SET group_count = group_count + 1,
			payload = $3, updated_at = clock_timestamp(), read_at = NULL
			WHERE id = (SELECT id FROM notifications WHERE user_id = $1 AND group_key = $2 AND deleted_at IS NULL
				AND updated_at > clock_timestamp() - $4::interval ORDER BY updated_at DESC LIMIT 1)
			RETURNING `+_cols, n.UserID, *n.GroupKey, payloadOf(n), intervalOf(it.Window)))

		switch {
		case err == nil:
			return row, true, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return n, false, fmt.Errorf("group update: %w", err)
		}
	}

	row, err := scanNotification(tx.QueryRow(ctx, `INSERT INTO notifications
		(user_id, type, payload, link, group_key, source_event_id) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, source_event_id) DO NOTHING RETURNING `+_cols,
		n.UserID, string(n.Type), payloadOf(n), n.Link, n.GroupKey, eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Notification{UserID: n.UserID}, false, nil
	}

	if err != nil {
		return n, false, fmt.Errorf("insert notification: %w", err)
	}

	return row, false, nil
}

func intervalOf(d time.Duration) string {
	return strconv.FormatInt(d.Microseconds(), 10) + " microseconds"
}

// QueueEmail -.
func (r *Repo) QueueEmail(ctx context.Context, eventID string, e entity.Email) (bool, error) {
	queued := false

	err := r.inTx(ctx, "NotificationRepo - QueueEmail", func(tx pgx.Tx) error {
		fresh, err := markProcessed(ctx, tx, eventID)
		if err != nil || !fresh {
			return err
		}

		ids, err := insertEmails(ctx, tx, eventID, []entity.Email{e})
		queued = len(ids) == 1

		return err
	})
	if err != nil {
		return false, fmt.Errorf("NotificationRepo - QueueEmail: %w", err)
	}

	return queued, nil
}

// List -.
func (r *Repo) List(ctx context.Context, userID string, unreadOnly bool, after *repo.Cursor, limit int,
) ([]entity.Notification, error) {
	sql := `SELECT ` + _cols + ` FROM notifications WHERE user_id = $1 AND deleted_at IS NULL`
	args := []any{userID, limit}

	if unreadOnly {
		sql += ` AND read_at IS NULL`
	}

	if after != nil {
		sql += ` AND (updated_at, id) < ($3, $4)`

		args = append(args, after.UpdatedAt, after.ID)
	}

	rows, err := r.Pool.Query(ctx, sql+` ORDER BY updated_at DESC, id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("NotificationRepo - List: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.Notification, error) {
		return scanNotification(row)
	})
	if err != nil {
		return nil, fmt.Errorf("NotificationRepo - List - scan: %w", err)
	}

	return out, nil
}

// UnreadCount -.
func (r *Repo) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	if err := r.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND `+_unread, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("NotificationRepo - UnreadCount: %w", err)
	}

	return n, nil
}

// exists reports whether the user has the notification (deleted ones count when withDeleted).
func exists(ctx context.Context, tx pgx.Tx, userID, id string, withDeleted bool) (bool, error) {
	sql := `SELECT EXISTS (SELECT 1 FROM notifications WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`
	if withDeleted {
		sql = `SELECT EXISTS (SELECT 1 FROM notifications WHERE id = $1 AND user_id = $2)`
	}

	var ok bool
	if err := tx.QueryRow(ctx, sql, id, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("exists: %w", err)
	}

	return ok, nil
}

// MarkRead -.
func (r *Repo) MarkRead(ctx context.Context, userID, id string) error {
	err := r.inTx(ctx, "NotificationRepo - MarkRead", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE notifications SET read_at = now()
			WHERE id = $1 AND user_id = $2 AND `+_unread, id, userID)
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}

		if tag.RowsAffected() == 1 {
			return announceUnread(ctx, tx, userID)
		}

		ok, err := exists(ctx, tx, userID, id, false)
		if err == nil && !ok {
			err = entity.ErrNotFound
		}

		return err
	})
	if err != nil {
		return fmt.Errorf("NotificationRepo - MarkRead: %w", err)
	}

	return nil
}

// MarkAllRead -.
func (r *Repo) MarkAllRead(ctx context.Context, userID string, before time.Time) (int64, error) {
	var n int64

	err := r.inTx(ctx, "NotificationRepo - MarkAllRead", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE notifications SET read_at = now()
			WHERE user_id = $1 AND `+_unread+` AND updated_at <= $2`, userID, before)
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}

		if n = tag.RowsAffected(); n == 0 {
			return nil
		}

		return announceUnread(ctx, tx, userID)
	})
	if err != nil {
		return 0, fmt.Errorf("NotificationRepo - MarkAllRead: %w", err)
	}

	return n, nil
}

// SoftDelete -.
func (r *Repo) SoftDelete(ctx context.Context, userID, id string) error {
	err := r.inTx(ctx, "NotificationRepo - SoftDelete", func(tx pgx.Tx) error {
		var wasUnread bool

		err := tx.QueryRow(ctx, `UPDATE notifications n SET deleted_at = now()
			FROM (SELECT id, read_at FROM notifications WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL FOR UPDATE) o
			WHERE n.id = o.id RETURNING o.read_at IS NULL`, id, userID).Scan(&wasUnread)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			ok, err := exists(ctx, tx, userID, id, true)
			if err == nil && !ok {
				err = entity.ErrNotFound
			}

			return err
		case err != nil:
			return fmt.Errorf("update: %w", err)
		case wasUnread:
			return announceUnread(ctx, tx, userID)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("NotificationRepo - SoftDelete: %w", err)
	}

	return nil
}

// Purge -.
func (r *Repo) Purge(ctx context.Context, before time.Time, batch int) (int64, error) {
	var total int64

	for {
		tag, err := r.Pool.Exec(ctx, `DELETE FROM notifications WHERE id IN
			(SELECT id FROM notifications WHERE created_at < $1 LIMIT $2)`, before, batch)
		if err != nil {
			return total, fmt.Errorf("NotificationRepo - Purge: %w", err)
		}

		total += tag.RowsAffected()
		if tag.RowsAffected() < int64(batch) || ctx.Err() != nil {
			return total, nil
		}
	}
}
