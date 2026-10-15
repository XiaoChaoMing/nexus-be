package ratelimit_test

// Needs a Redis: NXL_TEST_REDIS_ADDR=localhost:6379 go test ./... (skipped otherwise).

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo/ratelimit"
	"github.com/stretchr/testify/require"
)

func TestCountAndHit(t *testing.T) { //nolint:paralleltest // skipped without Redis
	addr := os.Getenv("NXL_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("NXL_TEST_REDIS_ADDR not set")
	}

	ctx := context.Background()
	rdb, err := redis.New(ctx, addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rdb.Close() })

	r := ratelimit.New(rdb)
	user := uuid.NewString()

	n, ttl, err := r.Count(ctx, user)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, ttl)

	require.NoError(t, r.Hit(ctx, user, time.Hour))
	require.NoError(t, r.Hit(ctx, user, time.Minute)) // the window is not extended

	n, ttl, err = r.Count(ctx, user)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Greater(t, ttl, 59*time.Minute)
}
