// Package ratelimit counts reports per user and hour in Redis (key safety:report:rate:{userId}).
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	goredis "github.com/redis/go-redis/v9"
)

const _keyPrefix = "safety:report:rate:"

// Repo implements repo.RateLimiter.
type Repo struct {
	rdb *redis.Client
}

var _ repo.RateLimiter = (*Repo)(nil)

// New -.
func New(rdb *redis.Client) *Repo { return &Repo{rdb: rdb} }

// Count returns the reports filed in the current window and the time left in it.
func (r *Repo) Count(ctx context.Context, userID string) (int, time.Duration, error) {
	key := _keyPrefix + userID
	pipe := r.rdb.Pipeline()
	get := pipe.Get(ctx, key)
	ttl := pipe.PTTL(ctx, key)

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return 0, 0, fmt.Errorf("ratelimit - Count: %w", err)
	}

	n, err := strconv.Atoi(get.Val())
	if err != nil {
		return 0, 0, nil //nolint:nilerr // no key yet
	}

	return n, max(ttl.Val(), 0), nil
}

// Hit counts one report; the first one of a window starts it.
func (r *Repo) Hit(ctx context.Context, userID string, window time.Duration) error {
	key := _keyPrefix + userID
	pipe := r.rdb.TxPipeline()
	pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, window)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("ratelimit - Hit: %w", err)
	}

	return nil
}
