package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
)

var _ repo.UploadRepo = (*Repo)(nil)

const _uploadCols = `id::text, channel_id::text, user_id::text, vod_id::text, file_name, content_type, size_bytes, part_size,
	part_count, s3_upload_id, object_key, title, description, category_id::text, is_mature, visibility, status, parts,
	source_deleted_at, created_at, updated_at, expires_at`

func scanUpload(row scanner) (entity.Upload, error) {
	var (
		u      entity.Upload
		status string
		parts  []byte
	)

	err := row.Scan(&u.ID, &u.ChannelID, &u.UserID, &u.VODID, &u.FileName, &u.ContentType, &u.SizeBytes, &u.PartSize,
		&u.PartCount, &u.S3UploadID, &u.ObjectKey, &u.Title, &u.Description, &u.CategoryID, &u.IsMature, &u.Visibility,
		&status, &parts, &u.SourceDeletedAt, &u.CreatedAt, &u.UpdatedAt, &u.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Upload{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Upload{}, fmt.Errorf("scan upload: %w", err)
	}

	u.Status = entity.UploadStatus(status)

	// encoding/json turns the object's "1", "2"… keys into the map's int keys.
	if err = json.Unmarshal(parts, &u.Parts); err != nil {
		return entity.Upload{}, fmt.Errorf("scan upload - parts: %w", err)
	}

	if u.Parts == nil {
		u.Parts = map[int]entity.UploadPart{}
	}

	return u, nil
}

func collectUploads(rows pgx.Rows, op string) ([]entity.Upload, error) {
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.Upload, error) {
		return scanUpload(row)
	})
	if err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}

	return out, nil
}

// uploadQuota counts what entity.UploadLimits limits (q is the pool or the CreateUpload transaction).
func uploadQuota(ctx context.Context, q querier, channelID string) (entity.UploadQuota, error) {
	var out entity.UploadQuota

	err := q.QueryRow(ctx,
		`SELECT
		    (SELECT count(*) FROM video_uploads WHERE channel_id = $1 AND status = 'UPLOADING' AND expires_at > now())
		  + (SELECT count(*) FROM vods WHERE channel_id = $1 AND source = 'UPLOAD' AND status = 'PROCESSING'),
		    (SELECT count(*) FROM video_uploads WHERE channel_id = $1 AND created_at > now() - make_interval(secs => $2)),
		    (SELECT min(created_at) FROM video_uploads WHERE channel_id = $1 AND created_at > now() - make_interval(secs => $2))`,
		channelID, entity.UploadQuotaWindow.Seconds()).Scan(&out.Active, &out.Daily, &out.OldestDaily)
	if err != nil {
		return entity.UploadQuota{}, fmt.Errorf("Repo - upload quota: %w", err)
	}

	return out, nil
}

// UploadQuota -.
func (r *Repo) UploadQuota(ctx context.Context, channelID string) (entity.UploadQuota, error) {
	return uploadQuota(ctx, r.Pool, channelID)
}

// CreateUpload -.
func (r *Repo) CreateUpload(ctx context.Context, u entity.Upload, l entity.UploadLimits) (entity.Upload, error) {
	var out entity.Upload

	err := r.inTx(ctx, "Repo - CreateUpload", func(tx pgx.Tx) error {
		// Serializes the quota check of one channel: two requests cannot both take the last slot.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "upload:"+u.ChannelID); err != nil {
			return fmt.Errorf("Repo - CreateUpload - lock: %w", err)
		}

		q, err := uploadQuota(ctx, tx, u.ChannelID)
		if err != nil {
			return err
		}

		if qerr := q.Check(l, time.Now()); qerr != nil {
			return qerr
		}

		out, err = scanUpload(tx.QueryRow(ctx,
			`INSERT INTO video_uploads (id, channel_id, user_id, file_name, content_type, size_bytes, part_size, part_count,
			                            s3_upload_id, object_key, title, description, category_id, is_mature, visibility,
			                            expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			 RETURNING `+_uploadCols,
			u.ID, u.ChannelID, u.UserID, u.FileName, u.ContentType, u.SizeBytes, u.PartSize, u.PartCount, u.S3UploadID,
			u.ObjectKey, u.Title, u.Description, u.CategoryID, u.IsMature, u.Visibility, u.ExpiresAt))
		if err != nil {
			return fmt.Errorf("Repo - CreateUpload - insert: %w", err)
		}

		return nil
	})

	return out, err
}

// GetUpload -.
func (r *Repo) GetUpload(ctx context.Context, channelID, uploadID string) (entity.Upload, error) {
	u, err := scanUpload(r.Pool.QueryRow(ctx,
		`SELECT `+_uploadCols+` FROM video_uploads WHERE id = $1 AND channel_id = $2`, uploadID, channelID))
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return entity.Upload{}, fmt.Errorf("Repo - GetUpload: %w", err)
	}

	return u, err
}

// SavePart merges the part into parts in one statement, so parts sent concurrently do not overwrite each other.
func (r *Repo) SavePart(ctx context.Context, uploadID string, n int, p entity.UploadPart) (entity.Upload, error) {
	u, err := scanUpload(r.Pool.QueryRow(ctx,
		`UPDATE video_uploads
		 SET parts = parts || jsonb_build_object($2::text, jsonb_build_object('etag', $3::text, 'size', $4::bigint)),
		     updated_at = now()
		 WHERE id = $1 AND status = 'UPLOADING'
		 RETURNING `+_uploadCols, uploadID, strconv.Itoa(n), p.ETag, p.Size))
	if errors.Is(err, entity.ErrNotFound) {
		u, err = scanUpload(r.Pool.QueryRow(ctx, `SELECT `+_uploadCols+` FROM video_uploads WHERE id = $1`, uploadID))
	}

	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return entity.Upload{}, fmt.Errorf("Repo - SavePart: %w", err)
	}

	return u, err
}

func uploadCompleted(u entity.Upload, vodID, bucket string) events.UploadCompleted {
	return events.UploadCompleted{
		UploadID: u.ID, VODID: vodID, ChannelID: u.ChannelID, Bucket: bucket, ObjectKey: u.ObjectKey, SizeBytes: u.SizeBytes,
	}
}

// CompleteUpload -.
func (r *Repo) CompleteUpload(ctx context.Context, uploadID, bucket string) (entity.Upload, error) {
	var out entity.Upload

	err := r.inTx(ctx, "Repo - CompleteUpload", func(tx pgx.Tx) error {
		u, err := scanUpload(tx.QueryRow(ctx, `SELECT `+_uploadCols+` FROM video_uploads WHERE id = $1 FOR UPDATE`, uploadID))

		switch {
		case errors.Is(err, entity.ErrNotFound):
			return err
		case err != nil:
			return fmt.Errorf("Repo - CompleteUpload - select: %w", err)
		case u.Status == entity.UploadCompleted:
			out = u

			return nil
		case u.Status != entity.UploadUploading:
			return entity.ErrUploadExpired
		}

		var vodID string

		if err = tx.QueryRow(ctx,
			`INSERT INTO vods (channel_id, source, title, description, category_id, is_mature, visibility, status, duration_sec)
			 VALUES ($1, 'UPLOAD', $2, $3, $4, $5, $6, 'PROCESSING', 0)
			 RETURNING id::text`,
			u.ChannelID, u.Title, u.Description, u.CategoryID, u.IsMature, u.Visibility).Scan(&vodID); err != nil {
			return fmt.Errorf("Repo - CompleteUpload - insert vod: %w", err)
		}

		out, err = scanUpload(tx.QueryRow(ctx,
			`UPDATE video_uploads SET status = 'COMPLETED', vod_id = $2, updated_at = now() WHERE id = $1
			 RETURNING `+_uploadCols, uploadID, vodID))
		if err != nil {
			return fmt.Errorf("Repo - CompleteUpload - update: %w", err)
		}

		return addOutbox(ctx, tx, vodID, events.TypeUploadCompleted, uploadCompleted(out, vodID, bucket))
	})

	return out, err
}

// AbortUpload -.
func (r *Repo) AbortUpload(ctx context.Context, channelID, uploadID string) (entity.Upload, bool, error) {
	u, err := scanUpload(r.Pool.QueryRow(ctx,
		`UPDATE video_uploads SET status = 'ABORTED', updated_at = now()
		 WHERE id = $1 AND channel_id = $2 AND status = 'UPLOADING'
		 RETURNING `+_uploadCols, uploadID, channelID))
	if err == nil {
		return u, true, nil
	}

	if !errors.Is(err, entity.ErrNotFound) {
		return entity.Upload{}, false, fmt.Errorf("Repo - AbortUpload: %w", err)
	}

	u, err = r.GetUpload(ctx, channelID, uploadID)

	return u, false, err
}

// ExpireUploads -.
func (r *Repo) ExpireUploads(ctx context.Context, limit int) ([]entity.Upload, error) {
	rows, err := r.Pool.Query(ctx,
		`UPDATE video_uploads SET status = 'EXPIRED', updated_at = now()
		 WHERE id IN (SELECT id FROM video_uploads WHERE status = 'UPLOADING' AND expires_at <= now()
		              ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		 RETURNING `+_uploadCols, limit)
	if err != nil {
		return nil, fmt.Errorf("Repo - ExpireUploads: %w", err)
	}

	return collectUploads(rows, "ExpireUploads")
}

// AbortChannelUploads -.
func (r *Repo) AbortChannelUploads(ctx context.Context, channelID string) ([]entity.Upload, error) {
	rows, err := r.Pool.Query(ctx,
		`UPDATE video_uploads SET status = 'ABORTED', updated_at = now()
		 WHERE channel_id = $1 AND status = 'UPLOADING'
		 RETURNING `+_uploadCols, channelID)
	if err != nil {
		return nil, fmt.Errorf("Repo - AbortChannelUploads: %w", err)
	}

	return collectUploads(rows, "AbortChannelUploads")
}

// SourcesToPurge -.
func (r *Repo) SourcesToPurge(ctx context.Context, limit int) ([]entity.Upload, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT `+_uploadCols+` FROM video_uploads
		 WHERE status = 'COMPLETED' AND source_deleted_at IS NULL
		   AND vod_id IN (SELECT id FROM vods WHERE source = 'UPLOAD' AND status IN ('READY', 'DELETED'))
		 ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("Repo - SourcesToPurge: %w", err)
	}

	return collectUploads(rows, "SourcesToPurge")
}

// MarkSourceDeleted -.
func (r *Repo) MarkSourceDeleted(ctx context.Context, uploadID string) error {
	if _, err := r.Pool.Exec(ctx,
		`UPDATE video_uploads SET source_deleted_at = now(), updated_at = now() WHERE id = $1 AND source_deleted_at IS NULL`,
		uploadID); err != nil {
		return fmt.Errorf("Repo - MarkSourceDeleted: %w", err)
	}

	return nil
}

// UploadOfVOD -.
func (r *Repo) UploadOfVOD(ctx context.Context, channelID, vodID string) (entity.Upload, error) {
	u, err := scanUpload(r.Pool.QueryRow(ctx,
		`SELECT `+_uploadCols+` FROM video_uploads WHERE vod_id = $1 AND channel_id = $2`, vodID, channelID))
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return entity.Upload{}, fmt.Errorf("Repo - UploadOfVOD: %w", err)
	}

	return u, err
}

// RetryVOD -.
func (r *Repo) RetryVOD(ctx context.Context, channelID, vodID, bucket string) (entity.VOD, error) {
	var v entity.VOD

	err := r.inTx(ctx, "Repo - RetryVOD", func(tx pgx.Tx) error {
		cur, err := scanVOD(tx.QueryRow(ctx,
			`SELECT `+_vodCols+` FROM vods WHERE id = $1 AND channel_id = $2 AND status <> 'DELETED' FOR UPDATE`,
			vodID, channelID))

		switch {
		case errors.Is(err, entity.ErrNotFound):
			return err
		case err != nil:
			return fmt.Errorf("Repo - RetryVOD - select vod: %w", err)
		case !cur.Retryable():
			return entity.ErrInvalidState
		}

		u, err := scanUpload(tx.QueryRow(ctx, `SELECT `+_uploadCols+` FROM video_uploads WHERE vod_id = $1`, vodID))

		switch {
		case errors.Is(err, entity.ErrNotFound):
			return entity.ErrInvalidState
		case err != nil:
			return fmt.Errorf("Repo - RetryVOD - select upload: %w", err)
		case u.SourceDeletedAt != nil:
			return entity.ErrInvalidState
		}

		v, err = scanVOD(tx.QueryRow(ctx,
			`UPDATE vods SET status = 'PROCESSING', failure_reason = NULL, failure_retryable = false, processing_percent = 0,
			        processing_stage = NULL, updated_at = now()
			 WHERE id = $1 RETURNING `+_vodCols, vodID))
		if err != nil {
			return fmt.Errorf("Repo - RetryVOD - update: %w", err)
		}

		return addOutbox(ctx, tx, vodID, events.TypeUploadCompleted, uploadCompleted(u, vodID, bucket))
	})

	return v, err
}

// SetProgress -.
func (r *Repo) SetProgress(ctx context.Context, vodID string, percent int, stage string) error {
	if _, err := r.Pool.Exec(ctx,
		`UPDATE vods SET processing_percent = $2, processing_stage = $3, updated_at = now()
		 WHERE id = $1 AND source = 'UPLOAD' AND status = 'PROCESSING' AND processing_percent <= $2`,
		vodID, percent, stage); err != nil {
		return fmt.Errorf("Repo - SetProgress: %w", err)
	}

	return nil
}

// MarkVODReady -.
func (r *Repo) MarkVODReady(ctx context.Context, in repo.UploadReadyInput) (repo.UploadOutcome, error) {
	out := repo.UploadOutcome{Result: repo.UploadResultIgnored}

	err := r.inTx(ctx, "Repo - MarkVODReady", func(tx pgx.Tx) error {
		cur, err := scanVOD(tx.QueryRow(ctx,
			`SELECT `+_vodCols+` FROM vods WHERE id = $1 AND source = 'UPLOAD' FOR UPDATE`, in.VODID))

		switch {
		case errors.Is(err, entity.ErrNotFound):
			return nil
		case err != nil:
			return fmt.Errorf("Repo - MarkVODReady - select: %w", err)
		}

		u, err := scanUpload(tx.QueryRow(ctx, `SELECT `+_uploadCols+` FROM video_uploads WHERE vod_id = $1`, in.VODID))
		if err != nil && !errors.Is(err, entity.ErrNotFound) {
			return fmt.Errorf("Repo - MarkVODReady - upload: %w", err)
		}

		switch cur.Status {
		case entity.VODProcessing:
			out = repo.UploadOutcome{Result: repo.UploadResultReady, Upload: u}

			return r.publishTx(ctx, tx, in)
		case entity.VODDeleted:
			out = repo.UploadOutcome{Result: repo.UploadResultDeleted, Upload: u}

			return addOutbox(ctx, tx, cur.ID, events.TypeVODDeleted, vodDeletedEvent(cur))
		}

		return nil
	})

	return out, err
}

// publishTx moves the PROCESSING upload to READY and queues vod.published.
func (r *Repo) publishTx(ctx context.Context, tx pgx.Tx, in repo.UploadReadyInput) error {
	v, err := scanVOD(tx.QueryRow(ctx,
		`UPDATE vods SET status = 'READY', duration_sec = $2, playlist_path = $3, thumbnail_url = NULLIF($4, ''),
		        published_at = now(), processing_percent = 100, processing_stage = NULL, failure_reason = NULL,
		        failure_retryable = false, updated_at = now()
		 WHERE id = $1 RETURNING `+_vodCols, in.VODID, in.DurationSec, in.PlaylistPath, in.ThumbnailPath))
	if err != nil {
		return fmt.Errorf("Repo - MarkVODReady - update: %w", err)
	}

	var publishedAt time.Time
	if v.PublishedAt != nil {
		publishedAt = v.PublishedAt.UTC()
	}

	return addOutbox(ctx, tx, v.ID, events.TypeVODPublished, events.VODPublished{
		VODID: v.ID, ChannelID: v.ChannelID, Source: v.Source, Title: v.Title, Description: v.Description,
		ThumbnailURL: r.objectURL(v.ThumbnailURL), DurationSec: v.DurationSec, CategoryID: v.CategoryID,
		IsMature: v.IsMature, Visibility: v.Visibility, PublishedAt: publishedAt,
	})
}

// MarkVODFailed -.
func (r *Repo) MarkVODFailed(ctx context.Context, vodID, reason string, retryable bool) (bool, error) {
	tag, err := r.Pool.Exec(ctx,
		`UPDATE vods SET status = 'FAILED', failure_reason = $2, failure_retryable = $3, processing_stage = NULL,
		        updated_at = now()
		 WHERE id = $1 AND source = 'UPLOAD' AND status = 'PROCESSING'`, vodID, reason, retryable)
	if err != nil {
		return false, fmt.Errorf("Repo - MarkVODFailed: %w", err)
	}

	return tag.RowsAffected() > 0, nil
}
