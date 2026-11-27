// Package repo declares stream-svc storage.
package repo

import (
	"context"
	"io"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
)

//go:generate mockgen -source=contracts.go -destination=../usecase/mocks_repo_test.go -package=usecase_test

type (
	// KeyRepo -.
	KeyRepo interface {
		// ChannelByKeyHash returns the channel of an active (not revoked) key.
		ChannelByKeyHash(ctx context.Context, keyHash string) (string, error)
		// Replace revokes the channel's active key and stores the new hash.
		Replace(ctx context.Context, channelID, keyHash string) (time.Time, error)
		Info(ctx context.Context, channelID string) (entity.StreamKeyInfo, error)
	}

	// SettingsRepo -.
	SettingsRepo interface {
		// GetSettings returns defaults when the channel never saved settings.
		GetSettings(ctx context.Context, channelID string) (entity.Settings, error)
		// SaveSettings upserts the settings and applies them to the channel's on-air stream, if any.
		SaveSettings(ctx context.Context, s entity.Settings) error
	}

	// StreamRepo owns the NXL-A §7A state machine. Every transition is a conditional UPDATE in one
	// transaction with its outbox event, so concurrent callers cannot both win.
	StreamRepo interface {
		// Authorize resumes an INTERRUPTED stream still inside grace, ends an expired one, attaches to the nearest
		// SCHEDULED stream inside [start - EarlyStart, start + LateGrace], or creates a LIVE stream.
		Authorize(ctx context.Context, channelID string, p AuthorizeParams) (entity.Grant, error)
		// End moves the channel's on-air stream to ENDED.
		End(ctx context.Context, channelID string, reason entity.EndReason) (entity.Stream, error)
		// ExpireInterrupted ends every INTERRUPTED stream older than grace.
		ExpireInterrupted(ctx context.Context, grace time.Duration) ([]entity.Stream, error)

		// Event handlers: eventID de-duplicates through processed_events (empty = no de-dup).
		OnIngestStarted(ctx context.Context, eventID, streamID string) error
		OnIngestStopped(ctx context.Context, eventID, streamID string) error
		OnVODProcessed(ctx context.Context, eventID string, v VODInput) error

		Get(ctx context.Context, streamID string) (entity.Stream, error)
		Current(ctx context.Context, channelID string) (*entity.Stream, error)
		ListOnAir(ctx context.Context, after *Cursor, limit int) ([]entity.Stream, error)
		// ListOnAirFiltered is ListOnAir (newest first, keyset) with filters.
		ListOnAirFiltered(ctx context.Context, f StreamFilter, after *Cursor, limit int) ([]entity.Stream, error)
		// ListOnAirAll returns at most max streams on air matching f, unordered (sorted by viewers in memory).
		ListOnAirAll(ctx context.Context, f StreamFilter, maxRows int) ([]entity.Stream, error)
		// GetVOD returns a READY, unexpired VOD (an uploaded one never expires) whatever its visibility.
		GetVOD(ctx context.Context, vodID string) (entity.VOD, error)

		// Scheduled streams (schedule.* events of channel-svc); eventID de-duplicates.
		CreateScheduled(ctx context.Context, eventID string, e events.ScheduleOccurrenceUpcoming) error
		ChangeScheduled(ctx context.Context, eventID string, e events.ScheduleOccurrenceChanged) error
		RetitleScheduled(ctx context.Context, eventID string, e events.ScheduleUpdated) error
		// CancelLateScheduled cancels SCHEDULED streams whose start is more than late ago; returns how many.
		CancelLateScheduled(ctx context.Context, late time.Duration, batch int) (int, error)

		DashboardStats(ctx context.Context, channelID string, since time.Time) (entity.DashboardStats, error)
		RecentEnded(ctx context.Context, channelID string, n int) ([]entity.RecentStream, error)

		// EndedByCreatorSince reports whether the channel's latest stream was ended by the creator after since.
		EndedByCreatorSince(ctx context.Context, channelID string, since time.Time) (bool, error)
		// ExpireVODs marks READY broadcast VODs past expires_at as EXPIRED (uploads never expire); returns how many.
		ExpireVODs(ctx context.Context) (int64, error)
		// CountOnAir returns how many streams are LIVE or INTERRUPTED.
		CountOnAir(ctx context.Context) (int, error)
		// SetPeakViewers keeps the highest peak reported by chat-svc; eventID de-duplicates.
		SetPeakViewers(ctx context.Context, eventID, streamID string, peak int) error
	}

	// VODRepo lists and edits VODs; edits queue vod.updated / vod.deleted in the same transaction.
	VODRepo interface {
		// ListPublic lists READY, PUBLIC, unexpired VODs, newest first; safeOnly leaves mature ones out.
		ListPublic(ctx context.Context, f VODFilter, safeOnly bool, after *VODCursor, limit int) ([]entity.VOD, error)
		// ListByChannel lists the channel's VODs: the public ones, or (public false) every READY / EXPIRED /
		// PROCESSING / FAILED one.
		ListByChannel(ctx context.Context, channelID string, public bool, f StudioVODFilter, after *VODCursor, limit int) ([]entity.VOD, error)
		// UpdateVOD returns entity.ErrNotFound (other channel, deleted) or entity.ErrInvalidState (entity.VOD.CanApply).
		UpdateVOD(ctx context.Context, channelID, vodID string, p entity.VODPatch) (entity.VOD, error)
		// DeleteVOD soft-deletes a READY, EXPIRED, PROCESSING or FAILED VOD; entity.ErrNotFound otherwise.
		DeleteVOD(ctx context.Context, channelID, vodID string) error
	}

	// UploadRepo stores upload sessions (video_uploads) and moves uploaded videos through PROCESSING → READY | FAILED.
	// Every VOD change queues its event in the same transaction.
	UploadRepo interface {
		// UploadQuota counts the channel's uploads in flight and the sessions it created over entity.UploadQuotaWindow.
		UploadQuota(ctx context.Context, channelID string) (entity.UploadQuota, error)
		// CreateUpload checks the quota again under a per-channel lock (*entity.UploadLimitError) and inserts u.
		CreateUpload(ctx context.Context, u entity.Upload, l entity.UploadLimits) (entity.Upload, error)
		// GetUpload returns entity.ErrNotFound for another channel's session.
		GetUpload(ctx context.Context, channelID, uploadID string) (entity.Upload, error)
		// SavePart records part n of an UPLOADING session and returns the session; a session that is no longer
		// UPLOADING is returned unchanged, so the caller sees its new status.
		SavePart(ctx context.Context, uploadID string, n int, p entity.UploadPart) (entity.Upload, error)
		// CompleteUpload creates the PROCESSING VOD, completes the session and queues upload.completed in one
		// transaction. A COMPLETED session is returned as is; an ABORTED / EXPIRED one is entity.ErrUploadExpired.
		CompleteUpload(ctx context.Context, uploadID, bucket string) (entity.Upload, error)
		// AbortUpload moves an UPLOADING session to ABORTED; ok is false when it was not UPLOADING.
		AbortUpload(ctx context.Context, channelID, uploadID string) (u entity.Upload, ok bool, err error)
		// ExpireUploads moves at most limit UPLOADING sessions past expires_at to EXPIRED and returns them.
		ExpireUploads(ctx context.Context, limit int) ([]entity.Upload, error)
		// AbortChannelUploads moves the channel's UPLOADING sessions to ABORTED and returns them.
		AbortChannelUploads(ctx context.Context, channelID string) ([]entity.Upload, error)
		// SourcesToPurge returns at most limit sessions whose VOD is READY or DELETED and whose source still exists.
		SourcesToPurge(ctx context.Context, limit int) ([]entity.Upload, error)
		// MarkSourceDeleted records that the source object was removed.
		MarkSourceDeleted(ctx context.Context, uploadID string) error
		// UploadOfVOD returns the session behind the channel's uploaded VOD (entity.ErrNotFound otherwise).
		UploadOfVOD(ctx context.Context, channelID, vodID string) (entity.Upload, error)
		// RetryVOD moves a FAILED, retryable VOD back to PROCESSING and queues upload.completed again;
		// entity.ErrInvalidState when it is not retryable.
		RetryVOD(ctx context.Context, channelID, vodID, bucket string) (entity.VOD, error)
		// SetProgress stores the transcoding progress of a PROCESSING upload; a lower report is ignored.
		SetProgress(ctx context.Context, vodID string, percent int, stage string) error
		// MarkVODReady applies upload.processed (see UploadOutcome).
		MarkVODReady(ctx context.Context, in UploadReadyInput) (UploadOutcome, error)
		// MarkVODFailed moves a PROCESSING upload to FAILED; ok is false when it was not PROCESSING.
		MarkVODFailed(ctx context.Context, vodID, reason string, retryable bool) (ok bool, err error)
	}

	// ObjectStore is the private bucket of upload sources (S3 multipart uploads).
	ObjectStore interface {
		// NewMultipart starts a multipart upload of key and returns its storage upload id.
		NewMultipart(ctx context.Context, key, contentType string) (string, error)
		// PutPart stores part n (size bytes read from body) and returns its ETag; sending n again replaces it.
		PutPart(ctx context.Context, key, uploadID string, n int, body io.Reader, size int64) (string, error)
		// CompleteMultipart assembles the parts; a multipart upload already completed into key is not an error.
		CompleteMultipart(ctx context.Context, key, uploadID string, parts []entity.UploadPartRef) error
		// AbortMultipart drops the parts; an unknown upload is not an error.
		AbortMultipart(ctx context.Context, key, uploadID string) error
		Exists(ctx context.Context, key string) (bool, error)
		// Remove deletes key; a missing key is not an error.
		Remove(ctx context.Context, key string) error
	}

	// ViewerCountRepo reads the live viewer counts chat-svc maintains (read only).
	ViewerCountRepo interface {
		// ViewerCounts returns the known counts among streamIDs; streams without one are absent.
		ViewerCounts(ctx context.Context, streamIDs []string) (map[string]int, error)
	}

	// ChannelRepo is the local copy of channel-svc's channels (creator_channels).
	ChannelRepo interface {
		// ChannelOfOwner returns entity.ErrNotFound when the user owns no channel.
		ChannelOfOwner(ctx context.Context, ownerID string) (entity.Channel, error)
		// ChannelsByID returns the known channels among ids in one query, keyed by channel id.
		ChannelsByID(ctx context.Context, ids []string) (map[string]entity.Channel, error)
		// UpsertChannel stores the channel of c.OwnerID; eventID de-duplicates (empty = no de-dup).
		UpsertChannel(ctx context.Context, eventID string, c entity.Channel) error
	}

	// SafetyRepo applies safety-svc's actions; eventID de-duplicates through processed_events.
	SafetyRepo interface {
		// Takedown ends an on-air stream with admin_takedown (stream.ended), marks it taken down so a later VOD
		// is created DELETED, and deletes its VODs (vod.deleted), in one transaction.
		Takedown(ctx context.Context, eventID, streamID string) (entity.TakedownResult, error)
		// SetSuspended stores the channel's suspension; suspending also ends its on-air stream (admin_takedown)
		// and cancels its SCHEDULED streams. Returns the streams it ended or cancelled.
		SetSuspended(ctx context.Context, eventID, channelID string, suspended bool) (ended, cancelled int, err error)
		// Streams pages through every stream (id > after, ordered by id) for other services' backfill.
		Streams(ctx context.Context, after string, limit int) ([]entity.StreamCopy, error)
		// TakedownVOD deletes the VOD whatever its state and queues vod.deleted; found is false when the VOD does
		// not exist or was already deleted.
		TakedownVOD(ctx context.Context, eventID, vodID string) (found bool, err error)
		// VODCopy returns any VOD, DELETED included (entity.ErrNotFound when unknown).
		VODCopy(ctx context.Context, vodID string) (entity.VOD, error)
	}

	// ViewerRepo stores what viewers do: playback sessions, mature consents and VOD positions.
	ViewerRepo interface {
		CreatePlaybackSession(ctx context.Context, s entity.PlaybackSession) error
		HasMatureConsent(ctx context.Context, userID, channelID string) (bool, error)
		// SaveMatureConsent is idempotent.
		SaveMatureConsent(ctx context.Context, userID, channelID string) error
		// SaveProgress upserts the position; keepFurther keeps the stored one when it is further.
		SaveProgress(ctx context.Context, p entity.WatchProgress, keepFurther bool) error
		// Progress returns nil when the user never watched the VOD.
		Progress(ctx context.Context, userID, vodID string) (*int, error)
		// ContinueWatching lists READY, unexpired VODs watched past MinResumeSec and not finished, newest first.
		ContinueWatching(ctx context.Context, userID string, limit int) ([]entity.ContinueItem, error)
		// PurgePlaybackSessions deletes sessions that expired before the given time; returns how many.
		PurgePlaybackSessions(ctx context.Context, before time.Time) (int64, error)
	}

	// VODInput -.
	VODInput struct {
		StreamID     string
		DurationSec  int
		PlaylistPath string
		ThumbnailURL string
		Retention    time.Duration
	}

	// Cursor is the keyset position of ListOnAir (started_at DESC, id DESC).
	Cursor struct {
		StartedAt time.Time
		ID        string
	}

	// VODCursor is the keyset position of VOD lists (created_at DESC, id DESC).
	VODCursor struct {
		CreatedAt time.Time
		ID        string
	}

	// AuthorizeParams are the time windows of Authorize.
	AuthorizeParams struct {
		Grace      time.Duration
		EarlyStart time.Duration
		LateGrace  time.Duration
	}

	// StreamFilter selects streams on air; empty fields do not filter.
	StreamFilter struct {
		CategoryIDs []string
		Language    string
		// Tags must all be present.
		Tags []string
	}

	// VODFilter selects public VODs.
	VODFilter struct {
		CategoryIDs []string
	}

	// StudioVODFilter selects the creator's VODs; nil does not filter.
	StudioVODFilter struct {
		Visibility *string
		Status     *string
		// Source is entity.SourceLive or entity.SourceUpload.
		Source *string
	}

	// UploadReadyInput is upload.processed: the HLS of an uploaded video is stored under videos/{vodId}/.
	UploadReadyInput struct {
		VODID         string
		DurationSec   int
		PlaylistPath  string
		ThumbnailPath string
	}

	// UploadOutcome says what MarkVODReady did.
	UploadOutcome struct {
		// Result is one of the UploadResult* values.
		Result string
		// Upload is the session behind the VOD (set unless Result is UploadResultIgnored).
		Upload entity.Upload
	}
)

// MarkVODReady results.
const (
	// UploadResultReady: the VOD went PROCESSING → READY and vod.published was queued.
	UploadResultReady = "READY"
	// UploadResultDeleted: the VOD was deleted while processing; vod.deleted was queued again so media-svc removes
	// the files it has just written.
	UploadResultDeleted = "DELETED"
	// UploadResultIgnored: unknown VOD, or already READY / FAILED (a redelivery).
	UploadResultIgnored = "IGNORED"
)
