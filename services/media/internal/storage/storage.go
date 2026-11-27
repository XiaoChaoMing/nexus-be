// Package storage puts HLS objects into S3-compatible storage (MinIO).
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

// StreamsPrefix holds every live and VOD object; viewers may read it, expiry applies to it.
const StreamsPrefix = "streams/"

// VideosPrefix holds the HLS of uploaded videos; viewers may read it, nothing expires there.
const VideosPrefix = "videos/"

// ObjectStore -.
type ObjectStore interface {
	Put(ctx context.Context, key string, body []byte, contentType, cacheControl string) error
}

// MinIO -.
type MinIO struct {
	client *minio.Client
	bucket string
}

// NewMinIO -.
func NewMinIO(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*MinIO, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage - NewMinIO - minio.New: %w", err)
	}

	return &MinIO{client: c, bucket: bucket}, nil
}

// Put -.
func (m *MinIO) Put(ctx context.Context, key string, body []byte, contentType, cacheControl string) error {
	_, err := m.client.PutObject(ctx, m.bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{
		ContentType:  contentType,
		CacheControl: cacheControl,
	})
	if err != nil {
		return fmt.Errorf("storage - Put %s: %w", key, err)
	}

	return nil
}

// RemovePrefix deletes every object under prefix and returns how many were removed (0 when none: idempotent).
func (m *MinIO) RemovePrefix(ctx context.Context, prefix string) (int, error) {
	var found []minio.ObjectInfo

	for obj := range m.client.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return 0, fmt.Errorf("storage - RemovePrefix %s - list: %w", prefix, obj.Err)
		}

		found = append(found, obj)
	}

	if len(found) == 0 {
		return 0, nil
	}

	objects := make(chan minio.ObjectInfo, len(found))
	for _, obj := range found {
		objects <- obj
	}

	close(objects)

	for rerr := range m.client.RemoveObjects(ctx, m.bucket, objects, minio.RemoveObjectsOptions{}) {
		if rerr.Err != nil {
			return 0, fmt.Errorf("storage - RemovePrefix %s - %s: %w", prefix, rerr.ObjectName, rerr.Err)
		}
	}

	return len(found), nil
}

// ErrObjectNotFound is returned by Download when the source object does not exist.
var ErrObjectNotFound = errors.New("storage: object not found")

// Download streams bucket/key into the file dst (created or truncated) and returns the bytes written.
func (m *MinIO) Download(ctx context.Context, bucket, key, dst string, perm os.FileMode) (int64, error) {
	obj, err := m.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("storage - Download %s/%s: %w", bucket, key, err)
	}
	defer obj.Close()

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, fmt.Errorf("storage - Download - create %s: %w", dst, err)
	}

	n, err := io.Copy(f, obj)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return n, fmt.Errorf("storage - Download %s/%s: %w", bucket, key, ErrObjectNotFound)
		}

		return n, fmt.Errorf("storage - Download %s/%s: %w", bucket, key, err)
	}

	return n, nil
}

// EnsureBucket creates the bucket, opens streams/ and videos/ for anonymous reads and expires streams/ after
// retentionDays.
func (m *MinIO) EnsureBucket(ctx context.Context, retentionDays int) error {
	exists, err := m.client.BucketExists(ctx, m.bucket)
	if err != nil {
		return fmt.Errorf("storage - EnsureBucket - BucketExists: %w", err)
	}

	if !exists {
		if err = m.client.MakeBucket(ctx, m.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("storage - EnsureBucket - MakeBucket: %w", err)
		}
	}

	// The edge reads MinIO anonymously and enforces signed URLs itself (secure_link), so both prefixes
	// must be readable here. No lifecycle rule ever covers videos/: uploaded videos do not expire.
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},`+
		`"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%[1]s/%[2]s*","arn:aws:s3:::%[1]s/%[3]s*"]}]}`,
		m.bucket, StreamsPrefix, VideosPrefix)
	if err = m.client.SetBucketPolicy(ctx, m.bucket, policy); err != nil {
		return fmt.Errorf("storage - EnsureBucket - SetBucketPolicy: %w", err)
	}

	if retentionDays > 0 {
		cfg := lifecycle.NewConfiguration()
		cfg.Rules = []lifecycle.Rule{{
			ID:         "expire-streams",
			Status:     "Enabled",
			RuleFilter: lifecycle.Filter{Prefix: StreamsPrefix},
			Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(retentionDays)},
		}}

		if err = m.client.SetBucketLifecycle(ctx, m.bucket, cfg); err != nil {
			return fmt.Errorf("storage - EnsureBucket - SetBucketLifecycle: %w", err)
		}
	}

	return nil
}
