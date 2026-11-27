// Package objects keeps upload source files in the private S3 bucket nxl-uploads (MinIO multipart uploads).
package objects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

const (
	// SourcesPrefix holds the uploaded source files (sources/{uploadId}).
	SourcesPrefix = "sources/"
	// A multipart upload nobody completed is dropped after a day (the session itself lasts STREAM_UPLOAD_TTL).
	_abortIncompleteDays = 1
	// Sources are deleted once transcoded; a FAILED upload keeps its source this long for a retry.
	_sourceRetentionDays = 3

	_codeNoSuchKey    = "NoSuchKey"
	_codeNoSuchUpload = "NoSuchUpload"
)

// Store implements repo.ObjectStore on MinIO; multipart calls go through minio.Core.
type Store struct {
	core   minio.Core
	bucket string
}

var _ repo.ObjectStore = (*Store)(nil)

// New -.
func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Store, error) {
	c, err := minio.NewCore(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("objects - New: %w", err)
	}

	return &Store{core: *c, bucket: bucket}, nil
}

// Bucket -.
func (s *Store) Bucket() string { return s.bucket }

// NewMultipart -.
func (s *Store) NewMultipart(ctx context.Context, key, contentType string) (string, error) {
	id, err := s.core.NewMultipartUpload(ctx, s.bucket, key, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("objects - NewMultipart %s: %w", key, err)
	}

	return id, nil
}

// PutPart -.
func (s *Store) PutPart(ctx context.Context, key, uploadID string, n int, body io.Reader, size int64) (string, error) {
	part, err := s.core.PutObjectPart(ctx, s.bucket, key, uploadID, n, body, size, minio.PutObjectPartOptions{})
	if err != nil {
		return "", fmt.Errorf("objects - PutPart %s #%d: %w", key, n, err)
	}

	return part.ETag, nil
}

// CompleteMultipart treats NoSuchUpload with the object present as done: an earlier call completed it and the
// session's database transaction failed afterwards.
func (s *Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []entity.UploadPartRef) error {
	complete := make([]minio.CompletePart, 0, len(parts))
	for _, p := range parts {
		complete = append(complete, minio.CompletePart{PartNumber: p.Number, ETag: p.ETag})
	}

	_, err := s.core.CompleteMultipartUpload(ctx, s.bucket, key, uploadID, complete, minio.PutObjectOptions{})
	if err == nil {
		return nil
	}

	if errorCode(err) == _codeNoSuchUpload {
		if ok, serr := s.Exists(ctx, key); serr == nil && ok {
			return nil
		}
	}

	return fmt.Errorf("objects - CompleteMultipart %s: %w", key, err)
}

// AbortMultipart -.
func (s *Store) AbortMultipart(ctx context.Context, key, uploadID string) error {
	err := s.core.AbortMultipartUpload(ctx, s.bucket, key, uploadID)
	if err != nil && errorCode(err) != _codeNoSuchUpload {
		return fmt.Errorf("objects - AbortMultipart %s: %w", key, err)
	}

	return nil
}

// Exists -.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.core.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}

	var resp minio.ErrorResponse
	if errors.As(err, &resp) && (resp.Code == _codeNoSuchKey || resp.StatusCode == http.StatusNotFound) {
		return false, nil
	}

	return false, fmt.Errorf("objects - Exists %s: %w", key, err)
}

// Remove -.
func (s *Store) Remove(ctx context.Context, key string) error {
	if err := s.core.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("objects - Remove %s: %w", key, err)
	}

	return nil
}

// EnsureBucket creates the private bucket and its lifecycle: incomplete multipart uploads are aborted after a day
// and sources/ expires after three days (a safety net behind the expiry job and the deletion after READY).
func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.core.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("objects - EnsureBucket - BucketExists: %w", err)
	}

	if !exists {
		if err = s.core.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("objects - EnsureBucket - MakeBucket: %w", err)
		}
	}

	expire := lifecycle.Rule{
		ID:         "expire-sources",
		Status:     "Enabled",
		RuleFilter: lifecycle.Filter{Prefix: SourcesPrefix},
		Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(_sourceRetentionDays)},
	}
	abort := lifecycle.Rule{
		ID:         "abort-incomplete-uploads",
		Status:     "Enabled",
		RuleFilter: lifecycle.Filter{Prefix: SourcesPrefix},
		AbortIncompleteMultipartUpload: lifecycle.AbortIncompleteMultipartUpload{
			DaysAfterInitiation: lifecycle.ExpirationDays(_abortIncompleteDays),
		},
	}

	cfg := lifecycle.NewConfiguration()
	cfg.Rules = []lifecycle.Rule{expire, abort}

	if err = s.core.SetBucketLifecycle(ctx, s.bucket, cfg); err == nil {
		return nil
	}

	// MinIO may refuse AbortIncompleteMultipartUpload; it drops stale multipart uploads by itself (24 h by default),
	// so the expiration rule alone is enough there.
	cfg.Rules = []lifecycle.Rule{expire}
	if lerr := s.core.SetBucketLifecycle(ctx, s.bucket, cfg); lerr != nil {
		return fmt.Errorf("objects - EnsureBucket - SetBucketLifecycle: %w (with abort rule: %w)", lerr, err)
	}

	return nil
}

func errorCode(err error) string {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code
	}

	return ""
}
