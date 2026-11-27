// Package uploads lets creators upload a video file in parts (S3 multipart) and follows its transcoding by media-svc.
package uploads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

// _jobBatch bounds the sessions one expiry or purge tick handles.
const _jobBatch = 100

// Config -.
type Config struct {
	// Bucket holds the source objects; media-svc reads them from there (upload.completed).
	Bucket   string
	MaxBytes int64
	PartSize int64
	// TTL is how long a session takes parts before it expires.
	TTL    time.Duration
	Limits entity.UploadLimits
}

// UseCase implements usecase.Uploads and usecase.UploadEvents.
type UseCase struct {
	r     repo.UploadRepo
	store repo.ObjectStore
	// enabled is false without object storage (STREAM_UPLOAD_ENABLED=false): background jobs skip storage then.
	enabled bool
	cfg     Config
	now     func() time.Time
	l       logger.Interface
}

var (
	_ usecase.Uploads      = (*UseCase)(nil)
	_ usecase.UploadEvents = (*UseCase)(nil)
)

// New -. A nil store turns uploads off: every call that needs storage returns entity.ErrStorageUnavailable, while
// the transcoding events of videos uploaded earlier are still applied.
func New(r repo.UploadRepo, store repo.ObjectStore, cfg Config, l logger.Interface) *UseCase {
	uc := &UseCase{r: r, store: store, enabled: store != nil, cfg: cfg, l: l, now: time.Now}
	if store == nil {
		uc.store = disabledStore{}
	}

	return uc
}

// WithClock replaces the time source; used by tests.
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

// storageError keeps the cause for the log and makes errors.Is(err, entity.ErrStorageUnavailable) hold.
func storageError(op string, err error) error {
	if errors.Is(err, entity.ErrStorageUnavailable) {
		return err
	}

	return fmt.Errorf("%w: UploadsUseCase - %s: %w", entity.ErrStorageUnavailable, op, err)
}

// Create -.
func (uc *UseCase) Create(ctx context.Context, ch entity.Channel, in usecase.NewUpload) (entity.Upload, error) {
	if ch.Suspended {
		return entity.Upload{}, entity.ErrChannelSuspended
	}

	if in.SizeBytes <= 0 || !entity.VideoFormatOK(in.FileName, in.ContentType) {
		return entity.Upload{}, entity.ErrUnsupportedFormat
	}

	if in.SizeBytes > uc.cfg.MaxBytes {
		return entity.Upload{}, &entity.FileTooLargeError{MaxBytes: uc.cfg.MaxBytes}
	}

	q, err := uc.r.UploadQuota(ctx, ch.ID)
	if err != nil {
		return entity.Upload{}, fmt.Errorf("UploadsUseCase - Create - quota: %w", err)
	}

	now := uc.now()
	if qerr := q.Check(uc.cfg.Limits, now); qerr != nil {
		return entity.Upload{}, qerr
	}

	u, err := uc.newUpload(ctx, ch.ID, in, now)
	if err != nil {
		return entity.Upload{}, err
	}

	out, err := uc.r.CreateUpload(ctx, u, uc.cfg.Limits)
	if err != nil {
		uc.abortQuietly(ctx, u)

		if errors.Is(err, entity.ErrUploadLimit) {
			return entity.Upload{}, err
		}

		return entity.Upload{}, fmt.Errorf("UploadsUseCase - Create: %w", err)
	}

	uc.l.Info("audit upload_created upload=%s channel=%s size=%d", out.ID, ch.ID, out.SizeBytes)

	return out, nil
}

// newUpload opens the multipart upload of a new session.
func (uc *UseCase) newUpload(ctx context.Context, channelID string, in usecase.NewUpload, now time.Time) (entity.Upload, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return entity.Upload{}, fmt.Errorf("UploadsUseCase - Create - uuid: %w", err)
	}

	contentType := strings.ToLower(strings.TrimSpace(in.ContentType))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	u := entity.Upload{
		ID: id.String(), ChannelID: channelID, UserID: in.UserID, FileName: in.FileName, ContentType: contentType,
		SizeBytes: in.SizeBytes, PartSize: uc.cfg.PartSize, PartCount: entity.PartCount(in.SizeBytes, uc.cfg.PartSize),
		ObjectKey: entity.UploadSourceKey(id.String()), Title: in.Title, Description: in.Description,
		CategoryID: in.CategoryID, IsMature: in.IsMature, Visibility: in.Visibility, Status: entity.UploadUploading,
		ExpiresAt: now.Add(uc.cfg.TTL),
	}

	if u.S3UploadID, err = uc.store.NewMultipart(ctx, u.ObjectKey, contentType); err != nil {
		return entity.Upload{}, storageError("Create", err)
	}

	return u, nil
}

// PutPart -.
func (uc *UseCase) PutPart(ctx context.Context, ch entity.Channel, uploadID string, n int, body io.Reader, size int64) error {
	u, err := uc.get(ctx, "PutPart", ch.ID, uploadID)
	if err != nil {
		return err
	}

	if ch.Suspended {
		return entity.ErrChannelSuspended
	}

	if rerr := u.Receiving(uc.now()); rerr != nil {
		return rerr
	}

	if perr := u.CheckPart(n, size); perr != nil {
		return perr
	}

	etag, err := uc.store.PutPart(ctx, u.ObjectKey, u.S3UploadID, n, body, size)
	if err != nil {
		return storageError("PutPart", err)
	}

	saved, err := uc.r.SavePart(ctx, uploadID, n, entity.UploadPart{ETag: etag, Size: size})
	if err != nil {
		return fmt.Errorf("UploadsUseCase - PutPart - save: %w", err)
	}

	// Aborted or expired while the part was on its way.
	if saved.Status != entity.UploadUploading {
		return saved.Receiving(uc.now())
	}

	return nil
}

// Get -.
func (uc *UseCase) Get(ctx context.Context, channelID, uploadID string) (entity.Upload, error) {
	return uc.get(ctx, "Get", channelID, uploadID)
}

func (uc *UseCase) get(ctx context.Context, op, channelID, uploadID string) (entity.Upload, error) {
	u, err := uc.r.GetUpload(ctx, channelID, uploadID)
	if errors.Is(err, entity.ErrNotFound) {
		return entity.Upload{}, err
	}

	if err != nil {
		return entity.Upload{}, fmt.Errorf("UploadsUseCase - %s: %w", op, err)
	}

	return u, nil
}

// Complete assembles the source object outside the transaction, then records the VOD. When the database fails after
// storage completed, a new call finds the object already assembled and finishes the job.
func (uc *UseCase) Complete(ctx context.Context, ch entity.Channel, uploadID string) (entity.Upload, error) {
	u, err := uc.get(ctx, "Complete", ch.ID, uploadID)
	if err != nil {
		return entity.Upload{}, err
	}

	if ch.Suspended {
		return entity.Upload{}, entity.ErrChannelSuspended
	}

	if u.Status == entity.UploadCompleted {
		return u, nil
	}

	if rerr := u.Receiving(uc.now()); rerr != nil {
		return entity.Upload{}, rerr
	}

	if missing := u.MissingParts(); len(missing) > 0 {
		return entity.Upload{}, &entity.PartsMissingError{Missing: missing}
	}

	if err = uc.store.CompleteMultipart(ctx, u.ObjectKey, u.S3UploadID, u.PartRefs()); err != nil {
		return entity.Upload{}, storageError("Complete", err)
	}

	out, err := uc.r.CompleteUpload(ctx, uploadID, uc.cfg.Bucket)
	if errors.Is(err, entity.ErrNotFound) || errors.Is(err, entity.ErrUploadExpired) {
		return entity.Upload{}, err
	}

	if err != nil {
		return entity.Upload{}, fmt.Errorf("UploadsUseCase - Complete: %w", err)
	}

	uc.l.Info("audit upload_completed upload=%s channel=%s vod=%s", out.ID, ch.ID, deref(out.VODID))

	return out, nil
}

// Abort -.
func (uc *UseCase) Abort(ctx context.Context, channelID, uploadID string) error {
	u, ok, err := uc.r.AbortUpload(ctx, channelID, uploadID)
	if errors.Is(err, entity.ErrNotFound) {
		return err
	}

	if err != nil {
		return fmt.Errorf("UploadsUseCase - Abort: %w", err)
	}

	if !ok {
		if u.Status == entity.UploadCompleted {
			return entity.ErrInvalidState
		}

		return nil
	}

	uc.abortQuietly(ctx, u)

	return nil
}

// abortQuietly drops the parts of a session that will never complete; on failure the bucket lifecycle drops them.
func (uc *UseCase) abortQuietly(ctx context.Context, u entity.Upload) {
	if !uc.enabled || u.S3UploadID == "" {
		return
	}

	if err := uc.store.AbortMultipart(ctx, u.ObjectKey, u.S3UploadID); err != nil {
		uc.l.Warn("uploads - abort multipart of %s: %v", u.ID, err)
	}
}

// Retry -.
func (uc *UseCase) Retry(ctx context.Context, ch entity.Channel, vodID string) (entity.VOD, error) {
	if ch.Suspended {
		return entity.VOD{}, entity.ErrChannelSuspended
	}

	u, err := uc.r.UploadOfVOD(ctx, ch.ID, vodID)

	switch {
	case errors.Is(err, entity.ErrNotFound):
		// Not an uploaded video of this channel: RetryVOD answers ErrNotFound or ErrInvalidState.
	case err != nil:
		return entity.VOD{}, fmt.Errorf("UploadsUseCase - Retry - upload: %w", err)
	case u.SourceDeletedAt == nil:
		ok, serr := uc.store.Exists(ctx, u.ObjectKey)
		if serr != nil {
			return entity.VOD{}, storageError("Retry", serr)
		}

		if !ok {
			return entity.VOD{}, entity.ErrInvalidState
		}
	}

	v, err := uc.r.RetryVOD(ctx, ch.ID, vodID, uc.cfg.Bucket)
	if errors.Is(err, entity.ErrNotFound) || errors.Is(err, entity.ErrInvalidState) {
		return entity.VOD{}, err
	}

	if err != nil {
		return entity.VOD{}, fmt.Errorf("UploadsUseCase - Retry: %w", err)
	}

	uc.l.Info("audit upload_retried vod=%s channel=%s", vodID, ch.ID)

	return v, nil
}

// ---- background jobs -----------------------------------------------------------------------------

// ExpireStale expires UPLOADING sessions past their TTL and drops their parts; returns how many.
func (uc *UseCase) ExpireStale(ctx context.Context) (int, error) {
	list, err := uc.r.ExpireUploads(ctx, _jobBatch)
	if err != nil {
		return 0, fmt.Errorf("UploadsUseCase - ExpireStale: %w", err)
	}

	for _, u := range list {
		uc.abortQuietly(ctx, u)
	}

	if len(list) > 0 {
		uc.l.Info("uploads - expired %d session(s)", len(list))
	}

	return len(list), nil
}

// PurgeSources deletes the source files of READY or DELETED uploads that are still stored; returns how many.
func (uc *UseCase) PurgeSources(ctx context.Context) (int, error) {
	if !uc.enabled {
		return 0, nil
	}

	list, err := uc.r.SourcesToPurge(ctx, _jobBatch)
	if err != nil {
		return 0, fmt.Errorf("UploadsUseCase - PurgeSources: %w", err)
	}

	n := 0

	for _, u := range list {
		if uc.discardSource(ctx, u) {
			n++
		}
	}

	return n, nil
}

// discardSource removes the source object and records it; a failure is retried by PurgeSources.
func (uc *UseCase) discardSource(ctx context.Context, u entity.Upload) bool {
	if !uc.enabled || u.ID == "" || u.SourceDeletedAt != nil {
		return false
	}

	if err := uc.store.Remove(ctx, u.ObjectKey); err != nil {
		uc.l.Warn("uploads - remove source of %s: %v", u.ID, err)

		return false
	}

	if err := uc.r.MarkSourceDeleted(ctx, u.ID); err != nil {
		uc.l.Warn("uploads - mark source of %s deleted: %v", u.ID, err)

		return false
	}

	return true
}

// ---- events --------------------------------------------------------------------------------------

// Progress -.
func (uc *UseCase) Progress(ctx context.Context, e events.UploadProgress) error {
	if err := uc.r.SetProgress(ctx, e.VODID, e.Percent, e.Stage); err != nil {
		return fmt.Errorf("UploadsUseCase - Progress: %w", err)
	}

	return nil
}

// Processed publishes the video, then deletes its source file (best effort, PurgeSources catches up).
func (uc *UseCase) Processed(ctx context.Context, e events.UploadProcessed) error {
	out, err := uc.r.MarkVODReady(ctx, repo.UploadReadyInput{
		VODID: e.VODID, DurationSec: int(math.Round(e.DurationSec)), PlaylistPath: e.PlaylistPath,
		ThumbnailPath: e.ThumbnailPath,
	})
	if err != nil {
		return fmt.Errorf("UploadsUseCase - Processed: %w", err)
	}

	switch out.Result {
	case repo.UploadResultReady:
		uc.l.Info("uploads - vod %s is ready", e.VODID)
		uc.discardSource(ctx, out.Upload)
	case repo.UploadResultDeleted:
		uc.l.Info("uploads - vod %s was deleted while processing: files removed again", e.VODID)
		uc.discardSource(ctx, out.Upload)
	default:
		uc.l.Debug("uploads - upload.processed for vod %s ignored (unknown or not processing)", e.VODID)
	}

	return nil
}

// Failed -.
func (uc *UseCase) Failed(ctx context.Context, e events.UploadFailed) error {
	ok, err := uc.r.MarkVODFailed(ctx, e.VODID, e.Reason, e.Retryable)
	if err != nil {
		return fmt.Errorf("UploadsUseCase - Failed: %w", err)
	}

	if ok {
		uc.l.Info("uploads - vod %s failed: %s (retryable %t)", e.VODID, e.Reason, e.Retryable)
	}

	return nil
}

// AbortChannel -.
func (uc *UseCase) AbortChannel(ctx context.Context, channelID string) error {
	list, err := uc.r.AbortChannelUploads(ctx, channelID)
	if err != nil {
		return fmt.Errorf("UploadsUseCase - AbortChannel: %w", err)
	}

	for _, u := range list {
		uc.abortQuietly(ctx, u)
	}

	if len(list) > 0 {
		uc.l.Info("uploads - channel %s suspended: %d upload(s) aborted", channelID, len(list))
	}

	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// disabledStore stands in for object storage when uploads are turned off.
type disabledStore struct{}

func (disabledStore) NewMultipart(context.Context, string, string) (string, error) {
	return "", entity.ErrStorageUnavailable
}

func (disabledStore) PutPart(context.Context, string, string, int, io.Reader, int64) (string, error) {
	return "", entity.ErrStorageUnavailable
}

func (disabledStore) CompleteMultipart(context.Context, string, string, []entity.UploadPartRef) error {
	return entity.ErrStorageUnavailable
}

func (disabledStore) AbortMultipart(context.Context, string, string) error {
	return entity.ErrStorageUnavailable
}

func (disabledStore) Exists(context.Context, string) (bool, error) {
	return false, entity.ErrStorageUnavailable
}

func (disabledStore) Remove(context.Context, string) error { return entity.ErrStorageUnavailable }
