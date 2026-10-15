// Package repo declares channel-svc storage.
package repo

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
)

type (
	// ChannelRepo stores channels. Event methods de-duplicate on eventID through processed_events
	// (empty = no de-dup) and write their outgoing event to the outbox in the same transaction.
	ChannelRepo interface {
		// ByHandle looks the handle up case-insensitively; entity.ErrNotFound when missing.
		ByHandle(ctx context.Context, handle string) (entity.Channel, error)
		ByID(ctx context.Context, id string) (entity.Channel, error)
		// ByOwner returns entity.ErrNotFound when the user owns no channel.
		ByOwner(ctx context.Context, ownerID string) (entity.Channel, error)
		// CreateForOwner inserts the owner's channel with the first free handle candidate of
		// in.BaseHandle and queues channel.created. created is false when the owner already has a
		// channel (returned as is, no event) or the event was already processed (zero channel).
		CreateForOwner(ctx context.Context, eventID string, in NewChannel) (ch entity.Channel, created bool, err error)
		// UpdateProfile applies p in one transaction (channel row, search documents, channel.updated when the
		// name or handle changed). entity.ErrHandleTaken when another channel holds the handle.
		UpdateProfile(ctx context.Context, channelID string, p entity.ProfilePatch, now time.Time) (entity.Channel, error)
		// SetImage stores the image URL and returns the previous one (channel.updated for the avatar).
		SetImage(ctx context.Context, channelID string, kind entity.ImageKind, url string) (old *string, err error)
		// SetLive marks the channel live and stores the stream copy and its search document.
		SetLive(ctx context.Context, eventID string, s entity.LiveStream) error
		// UpdateLive refreshes the stream copy of a stream on air.
		UpdateLive(ctx context.Context, eventID string, s entity.LiveStream) error
		// SetOffline clears the live flag unless a newer stream already replaced streamID.
		SetOffline(ctx context.Context, eventID, channelID, streamID string) error
		// LiveStreams returns the copies of the channels' streams on air, keyed by channel id.
		LiveStreams(ctx context.Context, channelIDs []string) (map[string]entity.LiveStream, error)
		// LiveStreamIDs lists every stream on air.
		LiveStreamIDs(ctx context.Context) ([]string, error)
	}

	// SafetyRepo applies safety-svc's channel actions.
	SafetyRepo interface {
		// SetStatus suspends or restores the channel once per event id, keeps its search documents in step
		// (removed while suspended, the channel's own restored) and queues channel.suspended / channel.restored;
		// changed is false when the channel was already in that state (or does not exist).
		SetStatus(ctx context.Context, eventID string, c entity.StatusChange) (changed bool, err error)
	}

	// SnapshotRepo pages through channels and follows for other services' one-off backfill (internal API).
	SnapshotRepo interface {
		// Channels returns up to limit channels with id > after, ordered by id.
		Channels(ctx context.Context, after string, limit int) ([]entity.ChannelCopy, error)
		// Follows returns up to limit follows after the (user, channel) position, ordered by it.
		Follows(ctx context.Context, after *entity.FollowCopy, limit int) ([]entity.FollowCopy, error)
	}

	// FollowRepo stores follows; changes queue follow.* in the same transaction.
	FollowRepo interface {
		// Follow is idempotent: created is false (and nothing is queued) when the user already follows.
		Follow(ctx context.Context, userID, channelID string) (f entity.Follow, created bool, err error)
		// SetNotifyLevel returns entity.ErrNotFollowing when there is no follow.
		SetNotifyLevel(ctx context.Context, userID, channelID string, l entity.NotifyLevel) (entity.Follow, error)
		Unfollow(ctx context.Context, userID, channelID string) error
		// Get returns nil when the user does not follow the channel.
		GetFollow(ctx context.Context, userID, channelID string) (*entity.Follow, error)
		// ListLive returns up to max followed channels that are live.
		ListLive(ctx context.Context, userID string, limit int) ([]entity.FollowedChannel, error)
		// ListFollowed returns followed channels, live first then by last broadcast (keyset after).
		ListFollowed(ctx context.Context, userID string, offlineOnly bool, after *FollowCursor, limit int) ([]entity.FollowedChannel, error)
		CountByUser(ctx context.Context, userID string) (int, error)
		NewFollowers(ctx context.Context, channelID string, since time.Time) (int, error)
	}

	// CategoryRepo reads the seeded categories.
	CategoryRepo interface {
		// Categories returns the categories by position, optionally of one group.
		Categories(ctx context.Context, group *entity.CategoryGroup) ([]entity.Category, error)
		BySlug(ctx context.Context, slug string) (entity.Category, error)
		ByIDs(ctx context.Context, ids []string) (map[string]entity.Category, error)
		// LiveStreamIDsByCategory maps each category to its streams on air.
		LiveStreamIDsByCategory(ctx context.Context) (map[string][]string, error)
	}

	// SearchRepo owns search_documents and search_history.
	SearchRepo interface {
		Suggest(ctx context.Context, q string, types []entity.DocType, limit int) ([]entity.SearchDoc, error)
		Search(ctx context.Context, q string, t entity.DocType, after *SearchCursor, limit int) ([]entity.SearchDoc, error)
		// UpsertVOD stores a public VOD's document or removes it when v.Public is false.
		UpsertVOD(ctx context.Context, eventID string, v entity.VODDoc) error
		DeleteVOD(ctx context.Context, eventID, vodID string) error
		// SetViewers stores the live viewer counts (keyed by stream id) on stream, channel and category documents.
		SetViewers(ctx context.Context, counts map[string]int) error
		History(ctx context.Context, userID string, limit int) ([]entity.SearchQuery, error)
		// Remember upserts the query (case-insensitive) and keeps only the newest keep entries.
		Remember(ctx context.Context, userID, query string, keep int) (entity.SearchQuery, error)
		// Forget removes one entry (id) or all of them (id nil).
		Forget(ctx context.Context, userID string, id *string) error
	}

	// ScheduleRepo stores schedules; every change queues its schedule.* events in the same transaction.
	ScheduleRepo interface {
		// Create returns entity.ErrScheduleLimit when the channel already has maxActive active schedules.
		Create(ctx context.Context, s entity.Schedule, maxActive int, now time.Time) (entity.Schedule, error)
		// Update stores s; when timing is true (start, rule or time zone changed) the announced and reminded
		// future occurrences of the old series are cancelled (SERIES_CHANGED).
		Update(ctx context.Context, s entity.Schedule, timing bool, now time.Time) (entity.Schedule, error)
		// Cancel cancels the schedule and its announced and reminded future occurrences.
		Cancel(ctx context.Context, s entity.Schedule, now time.Time) error
		// CancelOccurrence excludes at from the schedule.
		CancelOccurrence(ctx context.Context, s entity.Schedule, at time.Time) error
		// MoveOccurrence excludes at and creates the one-off moved (its reminders follow it).
		MoveOccurrence(ctx context.Context, s entity.Schedule, at time.Time, moved entity.Schedule) (entity.Schedule, error)
		// Get returns entity.ErrNotFound unless the schedule belongs to channelID.
		Get(ctx context.Context, channelID, id string) (entity.Schedule, error)
		GetPublic(ctx context.Context, id string) (entity.Schedule, error)
		ListByChannel(ctx context.Context, channelID string, s entity.ScheduleStatus) ([]entity.Schedule, error)
		OccurrenceStreams(ctx context.Context, scheduleIDs []string) (map[entity.OccurrenceKey]string, error)
		// OccurrenceLive reports whether the stream of the occurrence is on air.
		OccurrenceLive(ctx context.Context, scheduleID string, at time.Time) (bool, error)
		UpsertOccurrenceStream(ctx context.Context, eventID string, e events.StreamScheduled) error
		// AnnounceDue locks due schedules (SKIP LOCKED), queues the events fn returns for each and moves their
		// watermark to until; it returns how many events were queued.
		AnnounceDue(ctx context.Context, until time.Time, batch int,
			fn func(entity.Schedule) []events.ScheduleOccurrenceUpcoming) (int, error)
	}

	// ReminderRepo stores reminders.
	ReminderRepo interface {
		// Add is idempotent: created is false when the reminder existed.
		Add(ctx context.Context, r entity.Reminder) (entity.Reminder, bool, error)
		Remove(ctx context.Context, userID, scheduleID string, at time.Time) error
		Reminded(ctx context.Context, userID string, scheduleIDs []string) (map[entity.OccurrenceKey]bool, error)
		Counts(ctx context.Context, scheduleIDs []string, from time.Time) (map[entity.OccurrenceKey]int, error)
		// MarkDue marks reminders due within lead as notified and queues schedule.reminder_due in the same
		// transaction; it returns how many reminders were marked.
		MarkDue(ctx context.Context, lead time.Duration, batch int) (int, error)
	}

	// ViewerCountRepo reads the live viewer counts chat-svc maintains (Redis, read only).
	ViewerCountRepo interface {
		// ViewerCounts returns the known counts among streamIDs; streams without one are absent.
		ViewerCounts(ctx context.Context, streamIDs []string) (map[string]int, error)
	}

	// ObjectStore is the public bucket (nxl-public).
	ObjectStore interface {
		Put(ctx context.Context, key string, body []byte, contentType, cacheControl string) error
		Remove(ctx context.Context, key string) error
		Exists(ctx context.Context, key string) (bool, error)
	}

	// NewChannel -.
	NewChannel struct {
		OwnerID     string
		BaseHandle  string
		DisplayName string
	}

	// FollowCursor is the keyset position of List (is_live, last_live_at, channel id; all descending).
	FollowCursor struct {
		IsLive     bool
		LastLiveAt time.Time
		ChannelID  string
	}

	// SearchCursor is the keyset position of Search (all descending).
	SearchCursor struct {
		PrefixHit bool
		IsLive    bool
		Rank      int
		Viewers   int
		ID        string
	}
)
