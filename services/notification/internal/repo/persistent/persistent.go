// Package persistent implements notification-svc storage on PostgreSQL (database nxl_notification).
package persistent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
)

const (
	_consumer  = "notification-svc"
	_aggregate = "notification"
	// _unread is the condition of BR-E16 (index notifications_unread_idx).
	_unread = `read_at IS NULL AND deleted_at IS NULL`
)

// Repo implements the storage contracts of notification-svc.
type Repo struct {
	*postgres.Postgres
}

var (
	_ repo.NotificationRepo = (*Repo)(nil)
	_ repo.SettingRepo      = (*Repo)(nil)
	_ repo.RecipientRepo    = (*Repo)(nil)
	_ repo.ReplicaRepo      = (*Repo)(nil)
	_ repo.EmailRepo        = (*Repo)(nil)
)

// New -.
func New(pg *postgres.Postgres) *Repo { return &Repo{Postgres: pg} }

func (r *Repo) inTx(ctx context.Context, op string, fn func(tx pgx.Tx) error) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s - begin: %w", op, err)
	}

	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s - commit: %w", op, err)
	}

	return nil
}

// markProcessed records the event for this consumer; false means it was handled before.
func markProcessed(ctx context.Context, tx pgx.Tx, eventID string) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO processed_events (event_id, consumer) VALUES ($1, $2) ON CONFLICT DO NOTHING`, eventID, _consumer)
	if err != nil {
		return false, fmt.Errorf("processed_events: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

func addOutbox(ctx context.Context, tx pgx.Tx, aggregateID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox marshal: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES ($1, $2, $3, $4)`,
		_aggregate, aggregateID, eventType, b)
	if err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}

	return nil
}

// unreadCounts counts the unread notifications of users inside tx.
func unreadCounts(ctx context.Context, tx pgx.Tx, userIDs []string) (map[string]int, error) {
	rows, err := tx.Query(ctx, `SELECT user_id::text, count(*) FROM notifications
		WHERE user_id = ANY($1::uuid[]) AND `+_unread+` GROUP BY user_id`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("unread counts: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int, len(userIDs))

	for rows.Next() {
		var (
			id string
			n  int
		)

		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("unread counts - scan: %w", err)
		}

		out[id] = n
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unread counts - rows: %w", err)
	}

	return out, nil
}

// announce queues notification.created for each row, with the users' unread counts after the change.
func announce(ctx context.Context, tx pgx.Tx, rows []entity.Notification) error {
	if len(rows) == 0 {
		return nil
	}

	ids := make([]string, 0, len(rows))
	for _, n := range rows {
		ids = append(ids, n.UserID)
	}

	counts, err := unreadCounts(ctx, tx, ids)
	if err != nil {
		return err
	}

	for _, n := range rows {
		err := addOutbox(ctx, tx, n.ID, events.TypeNotificationCreated, events.NotificationCreated{
			NotificationID: n.ID, UserID: n.UserID, Type: string(n.Type), Payload: n.Payload, Link: n.Link,
			GroupCount: n.GroupCount, Read: n.Read(), CreatedAt: n.CreatedAt.UTC(), UpdatedAt: n.UpdatedAt.UTC(),
			UnreadCount: counts[n.UserID],
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// announceUnread queues notification.unread_changed with the user's count inside tx.
func announceUnread(ctx context.Context, tx pgx.Tx, userID string) error {
	counts, err := unreadCounts(ctx, tx, []string{userID})
	if err != nil {
		return err
	}

	return addOutbox(ctx, tx, userID, events.TypeNotificationUnreadChanged, events.NotificationUnreadChanged{
		UserID: userID, UnreadCount: counts[userID],
	})
}

// PurgeProcessed deletes processed_events rows older than before.
func (r *Repo) PurgeProcessed(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `DELETE FROM processed_events WHERE processed_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("Repo - PurgeProcessed: %w", err)
	}

	return tag.RowsAffected(), nil
}
