// Package usecase declares stream-svc use cases.
package usecase

import (
	"context"
	"io"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
)

type (
	// Studio is what a creator does with their own channel. channelID always comes from Channel.
	Studio interface {
		// Channel resolves the caller's channel; entity.ErrNotCreator when they own none.
		Channel(ctx context.Context, ownerID string) (entity.Channel, error)
		CreateKey(ctx context.Context, channelID string) (key string, err error)
		KeyInfo(ctx context.Context, channelID string) (entity.StreamKeyInfo, error)
		Overview(ctx context.Context, channelID string) (StudioOverview, error)
		UpdateSettings(ctx context.Context, s entity.Settings) (entity.Settings, error)
		End(ctx context.Context, channelID string) error
		// Dashboard returns entity.ErrInvalidState for an unknown range.
		Dashboard(ctx context.Context, channelID string, r entity.Range) (entity.Dashboard, error)
		StudioVODs(ctx context.Context, channelID string, f repo.StudioVODFilter, cursor string, limit int) (VODPage, error)
		// UpdateVOD returns entity.ErrNotFound or entity.ErrInvalidState (entity.VOD.CanApply).
		UpdateVOD(ctx context.Context, channelID, vodID string, p entity.VODPatch) (entity.VOD, error)
		DeleteVOD(ctx context.Context, channelID, vodID string) error
	}

	// Uploads is a creator sending a video file in parts from the Studio. ch is the caller's channel (Studio.Channel):
	// a suspended channel gets entity.ErrChannelSuspended; another channel's session is entity.ErrNotFound.
	Uploads interface {
		// Create checks format, size and quotas and opens the multipart upload.
		Create(ctx context.Context, ch entity.Channel, in NewUpload) (entity.Upload, error)
		// PutPart stores part n (size bytes of body); sending a part again replaces it.
		PutPart(ctx context.Context, ch entity.Channel, uploadID string, n int, body io.Reader, size int64) error
		Get(ctx context.Context, channelID, uploadID string) (entity.Upload, error)
		// Complete creates the PROCESSING VOD (Upload.VODID); calling it again returns the same session.
		Complete(ctx context.Context, ch entity.Channel, uploadID string) (entity.Upload, error)
		// Abort cancels an UPLOADING session (an aborted or expired one is fine); entity.ErrInvalidState once COMPLETED.
		Abort(ctx context.Context, channelID, uploadID string) error
		// Retry transcodes a FAILED, retryable upload again while its source file still exists.
		Retry(ctx context.Context, ch entity.Channel, vodID string) (entity.VOD, error)
	}

	// UploadEvents applies media-svc's transcoding results and channel suspensions to uploads. The handlers are
	// idempotent through the VOD's state (media-svc's event ids are not uuids).
	UploadEvents interface {
		Progress(ctx context.Context, e events.UploadProgress) error
		Processed(ctx context.Context, e events.UploadProcessed) error
		Failed(ctx context.Context, e events.UploadFailed) error
		// AbortChannel cancels the channel's sessions that are still UPLOADING.
		AbortChannel(ctx context.Context, channelID string) error
	}

	// NewUpload is what the creator declares before sending a file.
	NewUpload struct {
		UserID      string
		FileName    string
		ContentType string
		SizeBytes   int64
		Title       string
		Description string
		CategoryID  *string
		IsMature    bool
		Visibility  string
	}

	// Ingest handles media-svc: the authorize call and ingest events.
	Ingest interface {
		Authorize(ctx context.Context, streamKey, clientIP string) (entity.Grant, error)
		IngestStarted(ctx context.Context, eventID, streamID string) error
		IngestStopped(ctx context.Context, eventID, streamID string) error
		IngestHealth(streamID string, h entity.Health)
		VODProcessed(ctx context.Context, eventID string, v repo.VODInput) error
		// PeakViewers records the peak viewer count chat-svc measured for an ended stream.
		PeakViewers(ctx context.Context, eventID, streamID string, peak int) error
		// ExpireInterrupted ends streams whose grace period ran out; returns how many.
		ExpireInterrupted(ctx context.Context) (int, error)
		// ExpireVODs marks VODs past their retention as EXPIRED; returns how many.
		ExpireVODs(ctx context.Context) (int64, error)
	}

	// ChannelSync keeps the local copy of channel-svc's channels.
	ChannelSync interface {
		ChannelCreated(ctx context.Context, eventID string, c entity.Channel) error
		ChannelUpdated(ctx context.Context, eventID string, c entity.Channel) error
	}

	// Schedules turns channel-svc's schedule events into SCHEDULED streams and cancels the late ones.
	Schedules interface {
		OccurrenceUpcoming(ctx context.Context, eventID string, e events.ScheduleOccurrenceUpcoming) error
		OccurrenceChanged(ctx context.Context, eventID string, e events.ScheduleOccurrenceChanged) error
		ScheduleUpdated(ctx context.Context, eventID string, e events.ScheduleUpdated) error
		// CancelLate cancels SCHEDULED streams not started within the late grace; returns how many.
		CancelLate(ctx context.Context) (int, error)
	}

	// Safety applies safety-svc's actions and channel-svc's suspensions; idempotent on eventID.
	Safety interface {
		Takedown(ctx context.Context, eventID string, e events.StreamTakedown) error
		// VODTakedown deletes one VOD (uploaded or recorded); media-svc then removes its files.
		VODTakedown(ctx context.Context, eventID string, e events.VODTakedown) error
		ChannelSuspended(ctx context.Context, eventID string, e events.ChannelSuspended) error
		ChannelRestored(ctx context.Context, eventID string, e events.ChannelSuspended) error
	}

	// Snapshot pages through every stream for other services' backfill (internal API); next is "" on the last page.
	Snapshot interface {
		Streams(ctx context.Context, after string, limit int) (items []entity.StreamCopy, next string, err error)
		// VOD returns any VOD, DELETED included; entity.ErrNotFound when unknown.
		VOD(ctx context.Context, vodID string) (entity.VOD, error)
	}

	// Observer receives counters for /metrics; the use case never depends on a metrics library.
	Observer interface {
		Authorize(result string)
		Transition(to entity.Status)
		OnAir(n int)
	}

	// Catalog is the public read side; streams and VODs carry their channel when it is known. viewerID ""
	// is a guest; a hidden VOD is visible to its channel's owner only.
	Catalog interface {
		List(ctx context.Context, f StreamFilter, cursor string, limit int) (Page, error)
		// Recommended is the Hero / live recommendations: LIVE, not mature, by viewers.
		Recommended(ctx context.Context, limit int) ([]entity.Stream, error)
		GetFor(ctx context.Context, streamID, viewerID string) (entity.Stream, error)
		GetVODFor(ctx context.Context, vodID, viewerID string) (entity.VOD, error)
		VODs(ctx context.Context, f repo.VODFilter, cursor string, limit int) (VODPage, error)
		RecommendedVODs(ctx context.Context, limit int) ([]entity.VOD, error)
		// ChannelVODs returns entity.ErrNotFound for a channel stream-svc does not know; source "" is every source.
		ChannelVODs(ctx context.Context, channelID, source, cursor string, limit int) (VODPage, error)
	}

	// StreamSort -.
	StreamSort string

	// StreamFilter selects streams on air and their order.
	StreamFilter struct {
		repo.StreamFilter
		Sort StreamSort
	}

	// VODPage -.
	VODPage struct {
		Items      []entity.VOD
		NextCursor string
	}

	// PlaybackInput identifies the viewer opening a stream or VOD.
	PlaybackInput struct {
		Target entity.PlaybackTarget
		// UserID is empty for guests; AnonID is the guest's browser id (generated when empty).
		UserID            string
		AnonID            string
		AcknowledgeMature bool
	}

	// Viewer is what a viewer does around playback: open a session past the age gate and resume VODs.
	Viewer interface {
		Open(ctx context.Context, in PlaybackInput) (entity.PlaybackGrant, error)
		SaveProgress(ctx context.Context, p entity.WatchProgress, keepFurther bool) error
		// Progress returns nil when the user never watched the VOD.
		Progress(ctx context.Context, userID, vodID string) (*int, error)
		ContinueWatching(ctx context.Context, userID string, limit int) ([]entity.ContinueItem, error)
		// PurgeSessions removes expired playback sessions; returns how many.
		PurgeSessions(ctx context.Context) (int64, error)
	}

	// StudioOverview -.
	StudioOverview struct {
		Settings entity.Settings
		Current  *entity.Stream
		Health   *entity.Health
	}

	// Page -.
	Page struct {
		Items      []entity.Stream
		NextCursor string
	}
)

// Stream orders: viewers (default; trending is the same, G4-Q4) or newest.
const (
	SortViewers  StreamSort = "viewers"
	SortTrending StreamSort = "trending"
	SortNewest   StreamSort = "newest"
)

// Valid -.
func (s StreamSort) Valid() bool {
	return s == SortViewers || s == SortTrending || s == SortNewest
}
