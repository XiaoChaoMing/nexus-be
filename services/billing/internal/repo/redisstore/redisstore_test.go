package redisstore_test

// Needs a Redis: NXL_TEST_REDIS_ADDR=localhost:6379 go test ./... (skipped otherwise).

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/redisstore"
	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) { //nolint:paralleltest,funlen // skipped without Redis; one story
	addr := os.Getenv("NXL_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("NXL_TEST_REDIS_ADDR not set")
	}

	ctx := context.Background()
	rdb, err := redis.New(ctx, addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rdb.Close() })

	a, b := redisstore.New(rdb.Client), redisstore.New(rdb.Client)
	job := "test-" + uuid.NewString()

	release, ok, err := a.TryLock(ctx, job, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = b.TryLock(ctx, job, time.Minute)
	require.NoError(t, err)
	require.False(t, ok, "one instance at a time")

	release()

	releaseB, ok, err := b.TryLock(ctx, job, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)

	release() // a stale release must not free b's lock

	_, ok, err = a.TryLock(ctx, job, time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	releaseB()

	last, err := a.LastRun(ctx, job)
	require.NoError(t, err)
	require.Empty(t, last)
	require.NoError(t, a.SetLastRun(ctx, job, "2026-02-09"))
	last, err = b.LastRun(ctx, job)
	require.NoError(t, err)
	require.Equal(t, "2026-02-09", last)

	order := uuid.NewString()

	for want := 1; want <= 3; want++ {
		n, ttl, err := a.Hit(ctx, order)
		require.NoError(t, err)
		require.Equal(t, want, n)
		require.LessOrEqual(t, ttl, time.Minute)
		require.Positive(t, ttl)
	}

	inv := "NXLTEST" + uuid.NewString()[:6]
	require.NoError(t, a.Set(ctx, inv, map[string]string{"status": "PAID", "amount": "59000"}))
	require.NoError(t, a.Set(ctx, inv, map[string]string{"status": "CANCELLED"}))

	st, err := a.Get(ctx, inv)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"status": "CANCELLED"}, st, "replaced, not merged")

	invoices, err := a.Invoices(ctx)
	require.NoError(t, err)
	require.Contains(t, invoices, inv)
}
