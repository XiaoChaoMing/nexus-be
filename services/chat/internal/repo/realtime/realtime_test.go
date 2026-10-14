package realtime_test

// Integration tests against a real Redis (database 15 is flushed):
//   NXL_TEST_REDIS_ADDR=localhost:6379 go test ./...
// Skipped when the variable is not set.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/realtime"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T, o realtime.Options) *realtime.Store {
	t.Helper()

	addr := os.Getenv("NXL_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("NXL_TEST_REDIS_ADDR not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rdb, err := redis.New(ctx, "redis://"+addr+"/15")
	require.NoError(t, err)
	t.Cleanup(func() { _ = rdb.Close() }) // test teardown

	return realtime.New(rdb, o)
}

func ev(topic, typ string) entity.Event {
	return entity.Event{Topic: topic, Type: typ, Data: []byte(`{"n":1}`), TS: time.Now().UTC()}
}

func TestSeqAndResumeBuffer(t *testing.T) {
	t.Parallel()

	s := setup(t, realtime.Options{ResumeBuffer: 200, ResumeTTL: time.Minute})
	ctx := context.Background()
	topic := "chat:" + uuid.NewString()

	seq, err := s.Seq(ctx, topic)
	require.NoError(t, err)
	require.Zero(t, seq)

	evs, ok, err := s.Since(ctx, topic, 0, 10)
	require.NoError(t, err)
	require.True(t, ok, "nothing to replay is complete")
	require.Empty(t, evs)

	for i := 1; i <= 5; i++ {
		got, aerr := s.Append(ctx, ev(topic, "message"))
		require.NoError(t, aerr)
		require.Equal(t, int64(i), got.Seq)
	}

	evs, ok, err = s.Since(ctx, topic, 2, 10)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, evs, 3)
	require.Equal(t, int64(3), evs[0].Seq)
	require.JSONEq(t, `{"n":1}`, string(evs[0].Data))
	require.Equal(t, "message", evs[0].Type)
	require.Equal(t, topic, evs[0].Topic)

	_, ok, err = s.Since(ctx, topic, 9, 10)
	require.NoError(t, err)
	require.False(t, ok, "a client ahead of the counter (reset) must resync")
}

func TestResumeBufferIsTrimmed(t *testing.T) {
	t.Parallel()

	// MAXLEN ~ trims whole nodes; with 400 entries and a limit of 2 the oldest are gone.
	s := setup(t, realtime.Options{ResumeBuffer: 2, ResumeTTL: time.Minute})
	ctx := context.Background()
	topic := "chat:" + uuid.NewString()

	for range 400 {
		_, err := s.Append(ctx, ev(topic, "message"))
		require.NoError(t, err)
	}

	_, ok, err := s.Since(ctx, topic, 1, 10)
	require.NoError(t, err)
	require.False(t, ok)

	evs, ok, err := s.Since(ctx, topic, 399, 10)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, evs, 1)
}

func TestGuards(t *testing.T) {
	t.Parallel()

	s := setup(t, realtime.Options{DuplicateWindow: time.Minute})
	ctx := context.Background()
	user, stream, cmid := uuid.NewString(), uuid.NewString(), uuid.NewString()

	_, found, err := s.AcceptedMessage(ctx, user, cmid)
	require.NoError(t, err)
	require.False(t, found)

	require.NoError(t, s.RememberMessage(ctx, user, stream, cmid, "m1", "hash"))

	id, found, err := s.AcceptedMessage(ctx, user, cmid)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "m1", id)

	h, err := s.LastBodyHash(ctx, user, stream)
	require.NoError(t, err)
	require.Equal(t, "hash", h)

	key := "chat:rate:" + user
	for range 3 {
		ok, _, terr := s.Take(ctx, key, 3, 30*time.Second)
		require.NoError(t, terr)
		require.True(t, ok)
	}

	ok, retry, err := s.Take(ctx, key, 3, 30*time.Second)
	require.NoError(t, err)
	require.False(t, ok)
	require.InDelta(t, 10*time.Second, retry, float64(time.Second), "one token every 10 s")

	ok, _, err = s.StartSlow(ctx, stream, user, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)

	ok, left, err := s.StartSlow(ctx, stream, user, 5*time.Second)
	require.NoError(t, err)
	require.False(t, ok)
	require.InDelta(t, 5*time.Second, left, float64(time.Second))

	left, err = s.SlowLeft(ctx, stream, uuid.NewString())
	require.NoError(t, err)
	require.Zero(t, left)
}

func TestAudienceAndConnections(t *testing.T) {
	t.Parallel()

	s := setup(t, realtime.Options{})
	ctx := context.Background()
	ch, u1, u2 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	at := time.Now().Add(-time.Hour).Truncate(time.Second).UTC()

	require.NoError(t, s.SetFollow(ctx, ch, u1, at))
	require.NoError(t, s.SetSub(ctx, ch, u2, 2))

	followed, tier, err := s.Audience(ctx, ch, u1)
	require.NoError(t, err)
	require.Equal(t, at, *followed)
	require.Zero(t, tier)

	followed, tier, err = s.Audience(ctx, ch, u2)
	require.NoError(t, err)
	require.Nil(t, followed)
	require.Equal(t, 2, tier)

	tiers, err := s.SubTiers(ctx, ch, []string{u1, u2})
	require.NoError(t, err)
	require.Equal(t, map[string]int{u2: 2}, tiers)

	require.NoError(t, s.RemoveFollow(ctx, ch, u1))
	require.NoError(t, s.RemoveSub(ctx, ch, u2))

	followed, tier, err = s.Audience(ctx, ch, u1)
	require.NoError(t, err)
	require.Nil(t, followed)
	require.Zero(t, tier)

	for i := range 2 {
		ok, aerr := s.AddConn(ctx, u1, "c"+string(rune('0'+i)), 2)
		require.NoError(t, aerr)
		require.True(t, ok)
	}

	ok, err := s.AddConn(ctx, u1, "c9", 2)
	require.NoError(t, err)
	require.False(t, ok, "WS_MAX_CONN_PER_USER")

	ok, err = s.AddConn(ctx, u1, "c0", 2)
	require.NoError(t, err)
	require.True(t, ok, "a known connection re-registers")

	require.NoError(t, s.RemoveConn(ctx, u1, "c0"))

	ok, err = s.AddConn(ctx, u1, "c9", 2)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestViewers(t *testing.T) { //nolint:paralleltest,funlen // the leader key is global; one story
	s := setup(t, realtime.Options{})
	ctx := context.Background()
	stream := uuid.NewString()
	now := time.Now()

	require.NoError(t, s.TouchViewers(ctx, stream, []string{"u:1", "a:2"}, now.Add(-time.Minute)))
	require.NoError(t, s.TouchViewers(ctx, stream, []string{"u:1", "c:3"}, now))

	n, err := s.CountViewers(ctx, stream, now.Add(-25*time.Second))
	require.NoError(t, err)
	require.Equal(t, 2, n)

	streams, err := s.WatchedStreams(ctx)
	require.NoError(t, err)
	require.Contains(t, streams, stream)

	lead, err := s.LeadViewers(ctx, "inst-a", time.Second)
	require.NoError(t, err)

	other, err := s.LeadViewers(ctx, "inst-b", time.Second)
	require.NoError(t, err)
	require.False(t, lead && other, "one lease holder at a time")

	n, changed, err := s.RefreshCount(ctx, stream, now.Add(-25*time.Second), 30*time.Second)
	require.NoError(t, err)
	require.Equal(t, 2, n, "the stale guest is dropped")
	require.True(t, changed)

	_, changed, err = s.RefreshCount(ctx, stream, now.Add(-25*time.Second), 30*time.Second)
	require.NoError(t, err)
	require.False(t, changed)

	require.NoError(t, s.TouchViewers(ctx, stream, []string{"u:9"}, now))
	_, _, err = s.RefreshCount(ctx, stream, now.Add(-25*time.Second), 30*time.Second)
	require.NoError(t, err)

	peak, err := s.Peak(ctx, stream)
	require.NoError(t, err)
	require.Equal(t, 3, peak)

	require.NoError(t, s.ForgetStream(ctx, stream))

	n, err = s.CountViewers(ctx, stream, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Zero(t, n)

	require.NoError(t, s.SetStreamStatus(ctx, stream, "LIVE"))
	status, err := s.StreamStatus(ctx, stream)
	require.NoError(t, err)
	require.Equal(t, "LIVE", status)

	status, err = s.StreamStatus(ctx, uuid.NewString())
	require.NoError(t, err)
	require.Empty(t, status)
}
