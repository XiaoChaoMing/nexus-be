// Package objects stores public images (avatars, banners, category covers) in the S3 bucket nxl-public,
// which Nginx serves read-only under /media/.
package objects

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Store implements repo.ObjectStore on MinIO.
type Store struct {
	client *minio.Client
	bucket string
}

var _ repo.ObjectStore = (*Store)(nil)

// New -.
func New(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Store, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("objects - New: %w", err)
	}

	return &Store{client: c, bucket: bucket}, nil
}

// Put -.
func (s *Store) Put(ctx context.Context, key string, body []byte, contentType, cacheControl string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{
		ContentType:  contentType,
		CacheControl: cacheControl,
	})
	if err != nil {
		return fmt.Errorf("objects - Put %s: %w", key, err)
	}

	return nil
}

// Remove -.
func (s *Store) Remove(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("objects - Remove %s: %w", key, err)
	}

	return nil
}

// Exists -.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}

	var resp minio.ErrorResponse
	if errors.As(err, &resp) && (resp.Code == "NoSuchKey" || resp.StatusCode == 404) {
		return false, nil
	}

	return false, fmt.Errorf("objects - Exists %s: %w", key, err)
}

// EnsureBucket creates the bucket and lets anyone read its objects (the edge serves them under /media/).
func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("objects - EnsureBucket - BucketExists: %w", err)
	}

	if !exists {
		if err = s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("objects - EnsureBucket - MakeBucket: %w", err)
		}
	}

	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},`+
		`"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`, s.bucket)
	if err = s.client.SetBucketPolicy(ctx, s.bucket, policy); err != nil {
		return fmt.Errorf("objects - EnsureBucket - SetBucketPolicy: %w", err)
	}

	return nil
}
