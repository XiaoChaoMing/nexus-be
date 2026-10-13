package persistent_test

// Integration tests against a migrated nxl_channel database:
//   NXL_TEST_PG_URL_CHANNEL=postgres://user:pass@localhost:5432/nxl_channel?sslmode=disable go test ./...
// Skipped when the variable is not set.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) (*persistent.Repo, *postgres.Postgres) {
	t.Helper()

	url := os.Getenv("NXL_TEST_PG_URL_CHANNEL")
	if url == "" {
		t.Skip("NXL_TEST_PG_URL_CHANNEL not set")
	}

	pg, err := postgres.New(url, postgres.MaxPoolSize(4))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	_, err = pg.Pool.Exec(context.Background(), `TRUNCATE channels, outbox, processed_events CASCADE`)
	require.NoError(t, err)

	return persistent.New(pg), pg
}

func createdEvents(t *testing.T, pg *postgres.Postgres) []events.ChannelCreated {
	t.Helper()

	rows, err := pg.Pool.Query(context.Background(),
		`SELECT payload FROM outbox WHERE event_type = $1 ORDER BY created_at, id`, events.TypeChannelCreated)
	require.NoError(t, err)

	defer rows.Close()

	var out []events.ChannelCreated

	for rows.Next() {
		var (
			b []byte
			e events.ChannelCreated
		)

		require.NoError(t, rows.Scan(&b))
		require.NoError(t, json.Unmarshal(b, &e))

		out = append(out, e)
	}

	require.NoError(t, rows.Err())

	return out
}

func TestCreateForOwnerIdempotent(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	owner, ev := uuid.NewString(), uuid.NewString()
	in := repo.NewChannel{OwnerID: owner, BaseHandle: "minhnn", DisplayName: "Minh"}

	ch, created, err := r.CreateForOwner(ctx, ev, in)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "minhnn", ch.Handle)
	require.Equal(t, entity.StatusActive, ch.Status)
	require.False(t, ch.IsLive)

	// Double delivery of the same event: nothing happens.
	_, created, err = r.CreateForOwner(ctx, ev, in)
	require.NoError(t, err)
	require.False(t, created)

	// Another approval event for the same owner: the existing channel, no second event.
	again, created, err := r.CreateForOwner(ctx, uuid.NewString(), repo.NewChannel{OwnerID: owner, BaseHandle: "other", DisplayName: "x"})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, ch.ID, again.ID)

	evs := createdEvents(t, pg)
	require.Equal(t, []events.ChannelCreated{{ChannelID: ch.ID, OwnerID: owner, Handle: "minhnn", DisplayName: "Minh"}}, evs)

	got, err := r.ByHandle(ctx, "MinhNN")
	require.NoError(t, err, "lookup is case-insensitive")
	require.Equal(t, ch.ID, got.ID)

	_, err = r.ByHandle(ctx, "nobody")
	require.ErrorIs(t, err, entity.ErrNotFound)
}

func TestCreateForOwnerHandleCollision(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	long := strings.Repeat("a", entity.HandleMaxLen)

	var handles []string

	for range 3 {
		ch, created, err := r.CreateForOwner(ctx, uuid.NewString(), repo.NewChannel{OwnerID: uuid.NewString(), BaseHandle: "minhnn", DisplayName: "Minh"})
		require.NoError(t, err)
		require.True(t, created)

		handles = append(handles, ch.Handle)
	}

	require.Equal(t, []string{"minhnn", "minhnn2", "minhnn3"}, handles)

	// Collisions are case-insensitive (citext) and keep the handle within 25 characters.
	_, _, err := r.CreateForOwner(ctx, "", repo.NewChannel{OwnerID: uuid.NewString(), BaseHandle: long, DisplayName: "A"})
	require.NoError(t, err)

	ch, created, err := r.CreateForOwner(ctx, "", repo.NewChannel{OwnerID: uuid.NewString(), BaseHandle: long, DisplayName: "A"})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, strings.Repeat("a", entity.HandleMaxLen-1)+"2", ch.Handle)

	require.Len(t, createdEvents(t, pg), 5)
}

func TestCreateForOwnerConcurrentDelivery(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	owner := uuid.NewString()

	var wg sync.WaitGroup

	errs := make([]error, 4)

	for i := range errs {
		wg.Go(func() {
			_, _, errs[i] = r.CreateForOwner(ctx, uuid.NewString(), repo.NewChannel{OwnerID: owner, BaseHandle: "race", DisplayName: "R"})
		})
	}

	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}

	require.Len(t, createdEvents(t, pg), 1, "one channel and one event per owner")
}

func TestLiveState(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()

	ch, _, err := r.CreateForOwner(ctx, "", repo.NewChannel{OwnerID: uuid.NewString(), BaseHandle: "liveone", DisplayName: "L"})
	require.NoError(t, err)

	s1, s2, ev := uuid.NewString(), uuid.NewString(), uuid.NewString()

	require.NoError(t, r.SetLive(ctx, ev, ch.ID, s1))
	require.NoError(t, r.SetLive(ctx, ev, ch.ID, s1), "redelivery")

	got, err := r.ByHandle(ctx, "liveone")
	require.NoError(t, err)
	require.True(t, got.IsLive)
	require.Equal(t, s1, *got.CurrentStreamID)

	// A late stream.ended of an older stream does not clear the newer one.
	require.NoError(t, r.SetLive(ctx, uuid.NewString(), ch.ID, s2))
	require.NoError(t, r.SetOffline(ctx, uuid.NewString(), ch.ID, s1))

	got, err = r.ByHandle(ctx, "liveone")
	require.NoError(t, err)
	require.True(t, got.IsLive)

	require.NoError(t, r.SetOffline(ctx, uuid.NewString(), ch.ID, s2))

	got, err = r.ByHandle(ctx, "liveone")
	require.NoError(t, err)
	require.False(t, got.IsLive)
	require.Nil(t, got.CurrentStreamID)
}
