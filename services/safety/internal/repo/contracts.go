// Package repo declares the storage and client contracts of safety-svc.
package repo

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
)

type (
	// ReportFilter narrows the admin queue; empty slices match everything.
	ReportFilter struct {
		Statuses    []string
		TargetTypes []string
		Reasons     []string
	}

	// TimeCursor is a keyset position (created_at, id).
	TimeCursor struct {
		At time.Time
		ID string
	}

	// NameCursor is a keyset position (name, id) of the admin lookups.
	NameCursor struct {
		Name string
		ID   string
	}

	// AuditFilter -.
	AuditFilter struct {
		ActorID *string
		Action  *string
		// Actions keeps the rows whose action is one of these (with Action, both apply).
		Actions      []string
		ResourceType *string
		ResourceID   *string
		From, To     *time.Time
	}

	// Resolution closes reports.
	Resolution struct {
		Status            string
		Note              string
		ApplyToSameTarget bool
	}

	// LookupIDs are the ids whose copies a page of reports, actions or audit rows needs.
	LookupIDs struct {
		Users    []string
		Channels []string
		Streams  []string
	}

	// Lookup holds the copies found; a missing id has no entry.
	Lookup struct {
		Users    map[string]entity.UserCopy
		Channels map[string]entity.ChannelCopy
		Streams  map[string]entity.StreamCopy
	}

	// UserPatch updates a user copy; nil fields keep the stored value. A patch without Username only
	// updates an existing row.
	UserPatch struct {
		ID          string
		Username    *string
		DisplayName *string
		GlobalRole  *string
		Status      *string
		// LockedUntil is written whenever Status is set.
		LockedUntil *time.Time
		CreatedAt   *time.Time
	}

	// ChannelPatch updates a channel copy; a patch without Handle only updates an existing row.
	ChannelPatch struct {
		ID          string
		OwnerID     *string
		Handle      *string
		DisplayName *string
		AvatarURL   *string
		SetAvatar   bool
		Status      *string
		// SuspendedAt is written whenever Status is set.
		SuspendedAt *time.Time
	}

	// StreamPatch updates a stream copy; a new row without Status starts as ENDED.
	StreamPatch struct {
		ID         string
		ChannelID  string
		Title      *string
		Status     *string
		EndReason  *string
		VODID      *string
		VODDeleted bool
		StartedAt  *time.Time
		EndedAt    *time.Time
	}

	// ReportRepo -.
	ReportRepo interface {
		OpenDuplicate(ctx context.Context, reporterID, targetType, targetID string) (*entity.Report, error)
		// Insert returns created=false with the open duplicate when one was inserted concurrently.
		Insert(ctx context.Context, r entity.Report) (entity.Report, bool, error)
		List(ctx context.Context, f ReportFilter, after *TimeCursor, limit int) ([]entity.Report, error)
		Counts(ctx context.Context) (open, inReview int, err error)
		Get(ctx context.Context, id string) (entity.Report, error)
		Related(ctx context.Context, r entity.Report, limit int) ([]entity.Report, error)
		// Claim and Resolve are conditional UPDATEs with audit (+ outbox) in one transaction.
		Claim(ctx context.Context, adminID, id string) (entity.Report, error)
		Resolve(ctx context.Context, adminID, id string, in Resolution) (entity.Report, int, error)
	}

	// ActionRepo -.
	ActionRepo interface {
		// Record writes admin_actions + audit_logs + the safety.* outbox row in one transaction.
		Record(ctx context.Context, a entity.AdminAction, audit entity.AuditEntry, eventType string, payload any) (string, error)
		// ForReport lists actions on the report's target or on the user behind it, newest first.
		ForReport(ctx context.Context, r entity.Report, limit int) ([]entity.AdminAction, error)
		ReportExists(ctx context.Context, id string) (bool, error)
	}

	// AuditRepo -.
	AuditRepo interface {
		// Insert is idempotent on SourceEventID; promote also sets the user copy's role to ADMIN.
		Insert(ctx context.Context, e entity.AuditEntry, promote *string) error
		List(ctx context.Context, f AuditFilter, after *TimeCursor, limit int) ([]entity.AuditEntry, error)
	}

	// ReplicaRepo -.
	ReplicaRepo interface {
		User(ctx context.Context, id string) (entity.UserCopy, error)
		Channel(ctx context.Context, id string) (entity.ChannelCopy, error)
		Stream(ctx context.Context, id string) (entity.StreamCopy, error)
		Lookup(ctx context.Context, ids LookupIDs) (Lookup, error)
		SearchUsers(ctx context.Context, q string, status *string, after *NameCursor, limit int) ([]entity.UserCopy, error)
		SearchChannels(ctx context.Context, q string, status *string, after *NameCursor, limit int) ([]entity.ChannelCopy, error)
		// Patches record eventID in processed_events (when not empty) and skip rows synced after at.
		PatchUser(ctx context.Context, eventID string, at time.Time, p UserPatch) error
		PatchChannel(ctx context.Context, eventID string, at time.Time, p ChannelPatch) error
		PatchStream(ctx context.Context, eventID string, at time.Time, p StreamPatch) error
		BackfillDone(ctx context.Context, name string) (bool, error)
		MarkBackfilled(ctx context.Context, name string) error
	}

	// RateLimiter counts reports per user and hour (Redis).
	RateLimiter interface {
		Count(ctx context.Context, userID string) (n int, ttl time.Duration, err error)
		Hit(ctx context.Context, userID string, window time.Duration) error
	}

	// EvidenceClient calls chat-svc's internal API with exactly one of messageID / userID; ErrNotFound when
	// the room or the message does not exist.
	EvidenceClient interface {
		Evidence(ctx context.Context, streamID, messageID, userID string, limit int) ([]entity.EvidenceItem, error)
	}

	// SnapshotClient pages identity / channel / stream internal snapshots (backfill only).
	SnapshotClient interface {
		Users(ctx context.Context, after string, limit int) ([]Snapshot[UserPatch], string, error)
		Channels(ctx context.Context, after string, limit int) ([]Snapshot[ChannelPatch], string, error)
		Streams(ctx context.Context, after string, limit int) ([]Snapshot[StreamPatch], string, error)
	}

	// Snapshot is one row of a source snapshot and the time the source last changed it.
	Snapshot[T any] struct {
		Row       T
		UpdatedAt time.Time
	}
)
