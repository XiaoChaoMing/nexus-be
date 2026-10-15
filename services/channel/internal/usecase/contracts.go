// Package usecase declares channel-svc use cases.
package usecase

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
)

type (
	// Directory is the public read side of channels. viewerID "" is a guest.
	Directory interface {
		// Profile returns entity.ErrNotFound for an unknown or malformed handle and entity.ErrSuspended for a
		// suspended channel.
		Profile(ctx context.Context, handle, viewerID string) (entity.Profile, error)
		// ChannelSchedule lists the occurrences of the channel's active schedules in [from, to), ascending.
		ChannelSchedule(ctx context.Context, channelID, viewerID string, from, to time.Time) ([]entity.OccurrenceView, error)
	}

	// Follows is what a viewer does with channels they follow.
	Follows interface {
		Follow(ctx context.Context, userID, channelID string) (entity.Follow, error)
		SetNotifyLevel(ctx context.Context, userID, channelID string, l entity.NotifyLevel) (entity.Follow, error)
		Unfollow(ctx context.Context, userID, channelID string) error
		Following(ctx context.Context, userID string, f entity.FollowingFilter, cursor string, limit int) (FollowingPage, error)
		Sidebar(ctx context.Context, userID string, limit int) (entity.Sidebar, error)
	}

	// Studio is what a creator does with their own channel. ownerID is the caller.
	Studio interface {
		// Channel returns entity.ErrNotCreator when the caller owns no channel.
		Channel(ctx context.Context, ownerID string) (entity.Channel, error)
		UpdateProfile(ctx context.Context, ownerID string, p entity.ProfilePatch) (entity.Channel, error)
		SetImage(ctx context.Context, ownerID string, kind entity.ImageKind, data []byte) (url string, err error)
		Stats(ctx context.Context, ownerID string, r entity.StatsRange) (entity.ChannelStats, error)
		// NextHandleChangeAt is when the channel may change its handle again (nil = now).
		NextHandleChangeAt(ch entity.Channel) *time.Time
	}

	// Catalog serves the categories.
	Catalog interface {
		Categories(ctx context.Context, group *entity.CategoryGroup, sort entity.CategorySort) ([]entity.Category, error)
		Category(ctx context.Context, slug string) (entity.Category, error)
	}

	// Search serves suggestions, results and the signed-in user's history.
	Search interface {
		Suggest(ctx context.Context, q string, types []entity.DocType, limit int) ([]entity.SearchGroup, error)
		Search(ctx context.Context, q string, t entity.DocType, cursor string, limit int) (SearchPage, error)
		History(ctx context.Context, userID string) ([]entity.SearchQuery, error)
		Remember(ctx context.Context, userID, query string) (entity.SearchQuery, error)
		// Forget removes one entry (id) or all of them (id nil).
		Forget(ctx context.Context, userID string, id *string) error
	}

	// Schedules is the creator's broadcast schedule.
	Schedules interface {
		List(ctx context.Context, ownerID string, s entity.ScheduleStatus) ([]entity.ScheduleView, error)
		Create(ctx context.Context, ownerID string, in entity.ScheduleInput) (entity.ScheduleView, error)
		Update(ctx context.Context, ownerID, id string, in entity.ScheduleInput) (entity.ScheduleView, error)
		Cancel(ctx context.Context, ownerID, id string) error
		MoveOccurrence(ctx context.Context, ownerID, id string, at, newStart time.Time) (entity.ScheduleView, error)
		CancelOccurrence(ctx context.Context, ownerID, id string, at time.Time) error
	}

	// Reminders are viewers' requests to be reminded of an occurrence.
	Reminders interface {
		Remind(ctx context.Context, userID, scheduleID string, at time.Time) (r entity.Reminder, created bool, err error)
		Unremind(ctx context.Context, userID, scheduleID string, at time.Time) error
	}

	// Sync applies events of other services to channels; every method is idempotent on eventID.
	Sync interface {
		// CreatorApproved creates the creator's channel once (owner_id is unique).
		CreatorApproved(ctx context.Context, eventID string, in CreatorApproved) error
		StreamStarted(ctx context.Context, eventID string, s entity.LiveStream) error
		StreamUpdated(ctx context.Context, eventID string, s entity.LiveStream) error
		StreamEnded(ctx context.Context, eventID, channelID, streamID string) error
		StreamScheduled(ctx context.Context, eventID string, e events.StreamScheduled) error
		VODChanged(ctx context.Context, eventID string, v entity.VODDoc) error
		VODDeleted(ctx context.Context, eventID, vodID string) error
	}

	// Safety applies safety-svc's channel actions (safety.channel_actioned.v1); idempotent on eventID.
	Safety interface {
		ChannelActioned(ctx context.Context, eventID string, e events.ChannelActioned) error
	}

	// Snapshot pages through channels and follows for other services' backfill (internal API). after is the
	// nextAfter of the previous page ("" = first); the returned next is "" on the last page.
	Snapshot interface {
		Channels(ctx context.Context, after string, limit int) (items []entity.ChannelCopy, next string, err error)
		Follows(ctx context.Context, after string, limit int) (items []entity.FollowCopy, next string, err error)
	}

	// Jobs run in the background (app.go tickers).
	Jobs interface {
		// AnnounceUpcoming queues schedule.occurrence_upcoming for occurrences within the upcoming lead.
		AnnounceUpcoming(ctx context.Context) (int, error)
		// DueReminders queues schedule.reminder_due for reminders within the reminder lead.
		DueReminders(ctx context.Context) (int, error)
	}

	// CreatorApproved -.
	CreatorApproved struct {
		UserID      string
		Username    string
		DisplayName string
	}

	// FollowingPage -.
	FollowingPage struct {
		Items      []entity.FollowedChannel
		NextCursor string
	}

	// SearchPage -.
	SearchPage struct {
		Items      []entity.SearchDoc
		NextCursor string
	}
)
