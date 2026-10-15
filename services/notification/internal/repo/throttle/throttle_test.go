package throttle_test

// Needs a Redis: NXL_TEST_REDIS_ADDR=localhost:6379 go test ./... (skipped otherwise).

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo/throttle"
	"github.com/stretchr/testify/require"
)

func TestThrottle(t *testing.T) {
	t.Parallel()

	addr := os.Getenv("NXL_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("NXL_TEST_REDIS_ADDR not set")
	}

	ctx := context.Background()
	rdb, err := redis.New(ctx, addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rdb.Close() })

	th := throttle.New(rdb)
	ch, u1, u2, ev := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()

	inApp, email, err := th.LiveCooldowns(ctx, ch, []string{u1, u2})
	require.NoError(t, err)
	require.Empty(t, inApp)
	require.Empty(t, email)

	require.NoError(t, th.SetLiveCooldowns(ctx, ch, ev, []string{u1}, []string{u1, u2}, time.Minute, time.Hour))

	inApp, email, err = th.LiveCooldowns(ctx, ch, []string{u1, u2})
	require.NoError(t, err)
	require.Equal(t, map[string]string{u1: ev}, inApp)
	require.Equal(t, map[string]string{u1: ev, u2: ev}, email)

	ttl, err := rdb.TTL(ctx, "notify:live:"+u1+":"+ch).Result()
	require.NoError(t, err)
	require.InDelta(t, time.Minute.Seconds(), ttl.Seconds(), 2)

	days := map[string]string{u1: "20261015", u2: "20261015"}
	require.NoError(t, th.CountEmails(ctx, map[string]string{u1: "20261015"}))
	require.NoError(t, th.CountEmails(ctx, map[string]string{u1: "20261015"}))

	today, err := th.EmailsToday(ctx, days)
	require.NoError(t, err)
	require.Equal(t, map[string]int{u1: 2}, today)
}
