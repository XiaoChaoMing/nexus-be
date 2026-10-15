// Package usecase declares the business contracts of safety-svc.
package usecase

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
)

type (
	// ReportInput is POST /v1/reports.
	ReportInput struct {
		TargetType, TargetID, Reason string
		Note, StreamID               *string
	}

	// ResolveInput is POST /v1/admin/reports/{id}/resolve.
	ResolveInput struct {
		Status, Note      string
		ApplyToSameTarget bool
	}

	// ReportQuery is GET /v1/admin/reports; CSV values are split by the controller.
	ReportQuery struct {
		Statuses, TargetTypes, Reasons []string
		Cursor                         string
		Limit                          int
	}

	// DirectoryQuery is GET /v1/admin/users and /v1/admin/channels.
	DirectoryQuery struct {
		Q      string
		Status *string
		Cursor string
		Limit  int
	}

	// AuditQuery is GET /v1/admin/audit-log.
	AuditQuery struct {
		repo.AuditFilter

		// Actor is a username (an optional leading @ is ignored), used when ActorID is not set.
		Actor  string
		Cursor string
		Limit  int
	}

	// Page is one keyset page; Next is empty on the last one.
	Page[T any] struct {
		Items []T
		Next  string
	}

	// LockInput is POST /v1/admin/users/{id}/lock; DurationHours nil = until unlocked.
	LockInput struct {
		DurationHours *int
		Reason        string
		ReportID      *string
	}

	// ActionInput is the body of the other admin actions.
	ActionInput struct {
		Reason   string
		ReportID *string
	}

	// Reports files user reports.
	Reports interface {
		// File returns created=false with the open duplicate (200).
		File(ctx context.Context, reporterID string, in ReportInput) (r entity.Report, created bool, err error)
	}

	// AdminReports is the report queue.
	AdminReports interface {
		List(ctx context.Context, q ReportQuery) (Page[entity.ReportView], error)
		OpenCount(ctx context.Context) (open, inReview int, err error)
		Get(ctx context.Context, id string) (entity.ReportDetail, error)
		Claim(ctx context.Context, adminID, id string) (entity.ReportView, error)
		Resolve(ctx context.Context, adminID, id string, in ResolveInput) (r entity.ReportView, closed int, err error)
	}

	// AdminActions records admin decisions; the owning services apply them from the safety.* events.
	AdminActions interface {
		LockUser(ctx context.Context, adminID, userID string, in LockInput) (string, error)
		UnlockUser(ctx context.Context, adminID, userID string, in ActionInput) (string, error)
		SuspendChannel(ctx context.Context, adminID, channelID string, in ActionInput) (string, error)
		RestoreChannel(ctx context.Context, adminID, channelID string, in ActionInput) (string, error)
		TakedownStream(ctx context.Context, adminID, streamID string, in ActionInput) (string, error)
	}

	// Directory is the admin lookup on the copies.
	Directory interface {
		Users(ctx context.Context, q DirectoryQuery) (Page[entity.UserCopy], error)
		Channels(ctx context.Context, q DirectoryQuery) (Page[entity.ChannelCopy], error)
		Channel(ctx context.Context, id string) (entity.ChannelCopy, error)
	}

	// Audit is the append-only audit log.
	Audit interface {
		List(ctx context.Context, q AuditQuery) (Page[entity.AuditEntry], error)
		// Record stores the audit row of a domain event (SourceEventID is the idempotency key); promote is the
		// user an ADMIN_PROMOTED row makes admin in the copy.
		Record(ctx context.Context, e entity.AuditEntry, promote *string) error
	}

	// Replica keeps the user / channel / stream copies from events and the one-off backfill.
	Replica interface {
		PatchUser(ctx context.Context, eventID string, at time.Time, p repo.UserPatch) error
		PatchChannel(ctx context.Context, eventID string, at time.Time, p repo.ChannelPatch) error
		PatchStream(ctx context.Context, eventID string, at time.Time, p repo.StreamPatch) error
		Backfill(ctx context.Context) error
	}

	// Observer receives business metrics.
	Observer interface {
		ReportFiled(targetType, reason string)
	}
)
