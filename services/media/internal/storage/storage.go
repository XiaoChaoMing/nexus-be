// Package storage puts HLS objects into S3-compatible storage (MinIO).
package storage

import (
	"bytes"
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

// StreamsPrefix holds every live and VOD object; viewers may read it, expiry applies to it.
const StreamsPrefix = "streams/"

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

// EnsureBucket creates the bucket, opens streams/ for anonymous reads and expires it after retentionDays.
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

	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},`+
		`"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/%s*"]}]}`, m.bucket, StreamsPrefix)
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
