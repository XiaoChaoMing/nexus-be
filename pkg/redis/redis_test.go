package redis_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	_, err := redis.New(ctx, "127.0.0.1:1")
	require.Error(t, err, "nothing listens on port 1: gives up when ctx expires")

	_, err = redis.New(context.Background(), "redis://%zz")
	require.Error(t, err)

	addr := os.Getenv("NXL_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("NXL_TEST_REDIS_ADDR not set")
	}

	c, err := redis.New(context.Background(), addr, redis.PoolSize(2))
	require.NoError(t, err)
	require.NoError(t, c.Healthy(context.Background()))
	require.NoError(t, c.Close())
}
