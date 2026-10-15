// Package repo declares the storage, cache and client contracts notification-svc use cases depend on.
package repo

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
)

type (
	// Cursor is a position in a user's list, newest first: (updated_at, id).
	Cursor struct {
		UpdatedAt time.Time
		ID        string
	}

	// BatchResult names the recipients a fan-out batch wrote something for.
	BatchResult struct {
		// Notified got a new in-app row (a redelivery that conflicts on (user_id, source_event_id) is not listed).
		Notified []string
		// Emailed got a new email_outbox row.
		Emailed []string
	}

	// Pending is one notification of a small event, with its optional e-mail.
	Pending struct {
		Notification entity.Notification
		// Window > 0 groups into the newest row of the same (user, group key) updated within it.
		Window time.Duration
		// Email is queued only when a new row is inserted (a grouped update sends no second e-mail).
		Email *entity.Email
		// EmailOnly queues the e-mail without an in-app row (in-app turned off).
		EmailOnly bool
	}

	// Upserted is the outcome of one Pending.
	Upserted struct {
		Notification entity.Notification
		Grouped      bool
		Emailed      bool
	}

	// NotificationRepo stores notifications; every change and its notification.* outbox row share a transaction.
	NotificationRepo interface {
		// InsertBatch inserts n for each user id ON CONFLICT (user_id, source_event_id) DO NOTHING, the e-mails
		// (one per user and event), and notification.created rows with the users' unread counts.
		InsertBatch(ctx context.Context, eventID string, n entity.Notification, userIDs []string, emails []entity.Email,
		) (BatchResult, error)
		// Upsert applies the pendings of one small event once (processed_events); returns nil when the
		// event was already handled.
		Upsert(ctx context.Context, eventID string, items []Pending) ([]Upserted, error)
		// QueueEmail queues one e-mail without an in-app row, once per event.
		QueueEmail(ctx context.Context, eventID string, e entity.Email) (bool, error)
		List(ctx context.Context, userID string, unreadOnly bool, after *Cursor, limit int) ([]entity.Notification, error)
		UnreadCount(ctx context.Context, userID string) (int, error)
		// MarkRead, MarkAllRead and SoftDelete queue notification.unread_changed when the count changed.
		MarkRead(ctx context.Context, userID, id string) error
		MarkAllRead(ctx context.Context, userID string, before time.Time) (int64, error)
		SoftDelete(ctx context.Context, userID, id string) error
		// Purge deletes notifications created before the time, batch rows at a time.
		Purge(ctx context.Context, before time.Time, batch int) (int64, error)
		PurgeProcessed(ctx context.Context, before time.Time) (int64, error)
	}

	// SettingRepo stores the rows that differ from the defaults.
	SettingRepo interface {
		Overrides(ctx context.Context, userIDs []string) (map[string]map[entity.Type]entity.Setting, error)
		// Save upserts s and deletes the rows equal to the defaults.
		Save(ctx context.Context, userID string, s []entity.Setting) error
	}

	// RecipientRepo reads the copies.
	RecipientRepo interface {
		Channel(ctx context.Context, id string) (entity.ChannelCopy, error)
		// Followers pages the active followers of a channel with one of levels (keyset on user_id).
		Followers(ctx context.Context, channelID string, levels []string, excludeUserID, afterUserID string, limit int,
		) ([]entity.Recipient, error)
		Recipients(ctx context.Context, userIDs []string) (map[string]entity.Recipient, error)
		// BlockedBy returns which of userIDs blocked senderID.
		BlockedBy(ctx context.Context, senderID string, userIDs []string) (map[string]bool, error)
	}

	// Snapshot is one backfilled row and the source's last change.
	Snapshot[T any] struct {
		UpdatedAt time.Time
		Row       T
	}

	// ReplicaRepo writes the copies; a row synced after at is left alone.
	ReplicaRepo interface {
		PatchUser(ctx context.Context, at time.Time, u entity.UserCopy) error
		PatchChannel(ctx context.Context, at time.Time, c entity.ChannelPatch) error
		SetFollow(ctx context.Context, at time.Time, f entity.Follow) error
		RemoveFollow(ctx context.Context, at time.Time, userID, channelID string) error
		SetBlock(ctx context.Context, blockerID, blockedID string, blocked bool) error
		BackfillDone(ctx context.Context, name string) (bool, error)
		MarkBackfilled(ctx context.Context, name string) error
	}

	// SnapshotClient pages the internal snapshot APIs of identity and channel-svc (backfill only).
	SnapshotClient interface {
		Users(ctx context.Context, after string, limit int) ([]Snapshot[entity.UserCopy], string, error)
		Blocks(ctx context.Context, after string, limit int) ([][2]string, string, error)
		Channels(ctx context.Context, after string, limit int) ([]Snapshot[entity.ChannelPatch], string, error)
		Follows(ctx context.Context, after string, limit int) ([]entity.Follow, string, error)
	}

	// EmailRepo is the worker's side of email_outbox.
	EmailRepo interface {
		// Due leases up to limit PENDING rows whose next_attempt_at has passed (FOR UPDATE SKIP LOCKED) for
		// lease, so another worker does not pick them meanwhile.
		Due(ctx context.Context, limit int, lease time.Duration) ([]entity.OutboxEmail, error)
		MarkSent(ctx context.Context, id string) error
		// MarkFailed records a failed attempt: next nil marks the row FAILED.
		MarkFailed(ctx context.Context, id string, attempts int, next *time.Time, errText string) error
		PurgeDone(ctx context.Context, before time.Time) (int64, error)
	}

	// Throttle keeps the live cooldowns and daily e-mail counts in Redis (notify:*).
	Throttle interface {
		// LiveCooldowns returns, per user, the event id holding the in-app and e-mail cooldown of a channel.
		LiveCooldowns(ctx context.Context, channelID string, userIDs []string) (inApp, email map[string]string, err error)
		SetLiveCooldowns(ctx context.Context, channelID, eventID string, inApp, email []string, inAppTTL, emailTTL time.Duration,
		) error
		// EmailsToday returns the count per user for the given day keys (userID → yyyymmdd).
		EmailsToday(ctx context.Context, days map[string]string) (map[string]int, error)
		CountEmails(ctx context.Context, days map[string]string) error
	}

	// Mailer renders templates and sends messages over SMTP.
	Mailer interface {
		Render(template, locale, to string, data map[string]string) (entity.Message, error)
		// Send delivers msg; a mailer without SMTP returns entity.ErrMailDisabled.
		Send(ctx context.Context, msg entity.Message) error
	}
)
