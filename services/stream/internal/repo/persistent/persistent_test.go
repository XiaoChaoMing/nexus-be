package persistent_test

// Integration tests against a migrated nxl_stream database:
//   NXL_TEST_PG_URL_STREAM=postgres://user:pass@localhost:5432/nxl_stream?sslmode=disable go test ./...
// Skipped when the variable is not set.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) (*persistent.Repo, *postgres.Postgres) {
	t.Helper()

	url := os.Getenv("NXL_TEST_PG_URL_STREAM")
	if url == "" {
		t.Skip("NXL_TEST_PG_URL_STREAM not set")
	}

	pg, err := postgres.New(url, postgres.MaxPoolSize(4))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	_, err = pg.Pool.Exec(context.Background(),
		`TRUNCATE vods, streams, stream_keys, stream_settings, creator_channels, outbox, processed_events,
		 playback_sessions, watch_progress, mature_consents CASCADE`)
	require.NoError(t, err)

	return persistent.New(pg), pg
}

func outboxTypes(t *testing.T, pg *postgres.Postgres) []string {
	t.Helper()

	rows, err := pg.Pool.Query(context.Background(), `SELECT event_type FROM outbox ORDER BY created_at, id`)
	require.NoError(t, err)

	defer rows.Close()

	var out []string

	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}

	return out
}

func TestKeys(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()

	info, err := r.Info(ctx, ch)
	require.NoError(t, err)
	require.False(t, info.HasKey)

	_, err = r.Replace(ctx, ch, "hash-1")
	require.NoError(t, err)
	_, err = r.Replace(ctx, ch, "hash-2")
	require.NoError(t, err)

	_, err = r.ChannelByKeyHash(ctx, "hash-1")
	require.ErrorIs(t, err, entity.ErrInvalidStreamKey, "old key is revoked")

	got, err := r.ChannelByKeyHash(ctx, "hash-2")
	require.NoError(t, err)
	require.Equal(t, ch, got)
}

func TestLifecycle(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()
	grace := 2 * time.Second

	require.NoError(t, r.SaveSettings(ctx, entity.Settings{ChannelID: ch, Title: "Rank game", Tags: []string{"lol"}, Language: "vi"}))

	// First publish creates a LIVE stream with the saved settings.
	g, err := r.Authorize(ctx, ch, grace)
	require.NoError(t, err)
	require.False(t, g.Resumed)

	s, err := r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusLive, s.Status)
	require.Equal(t, "Rank game", s.Title)
	require.Equal(t, []string{"lol"}, s.Tags)

	// A second publish while LIVE is refused.
	_, err = r.Authorize(ctx, ch, grace)
	require.ErrorIs(t, err, entity.ErrAlreadyLive)

	// OBS drops → INTERRUPTED; a duplicate delivery of the same event is ignored.
	ev1 := uuid.NewString()
	require.NoError(t, r.OnIngestStopped(ctx, ev1, g.StreamID))
	require.NoError(t, r.OnIngestStopped(ctx, ev1, g.StreamID))

	s, err = r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusInterrupted, s.Status)
	require.NotNil(t, s.InterruptedAt)

	// Reconnect inside grace resumes the same stream; ingest.started puts it back to LIVE.
	g2, err := r.Authorize(ctx, ch, grace)
	require.NoError(t, err)
	require.True(t, g2.Resumed)
	require.Equal(t, g.StreamID, g2.StreamID)

	require.NoError(t, r.OnIngestStarted(ctx, uuid.NewString(), g.StreamID))

	s, err = r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusLive, s.Status)
	require.Nil(t, s.InterruptedAt)

	// Settings changed while live apply to the running stream.
	require.NoError(t, r.SaveSettings(ctx, entity.Settings{ChannelID: ch, Title: "Rank game 2", Tags: []string{}, Language: "vi"}))
	s, err = r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, "Rank game 2", s.Title)

	// Interrupted past grace: the expiry job ends it.
	require.NoError(t, r.OnIngestStopped(ctx, uuid.NewString(), g.StreamID))

	ended, err := r.ExpireInterrupted(ctx, grace)
	require.NoError(t, err)
	require.Empty(t, ended, "still inside grace")

	_, err = pg.Pool.Exec(ctx, `UPDATE streams SET interrupted_at = now() - interval '1 minute' WHERE id = $1`, g.StreamID)
	require.NoError(t, err)

	ended, err = r.ExpireInterrupted(ctx, grace)
	require.NoError(t, err)
	require.Len(t, ended, 1)
	require.Equal(t, entity.EndIngestTimeout, *ended[0].EndReason)

	// Late ingest events do not revive an ENDED stream.
	require.NoError(t, r.OnIngestStarted(ctx, uuid.NewString(), g.StreamID))
	s, err = r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusEnded, s.Status)

	// VOD arrives once, even if delivered twice.
	vodEvent := uuid.NewString()
	in := repo.VODInput{StreamID: g.StreamID, DurationSec: 42, PlaylistPath: "streams/" + g.StreamID + "/master.m3u8", Retention: time.Hour}
	require.NoError(t, r.OnVODProcessed(ctx, vodEvent, in))
	require.NoError(t, r.OnVODProcessed(ctx, uuid.NewString(), in)) // different id, same stream: ON CONFLICT

	s, err = r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.NotNil(t, s.VODID)

	v, err := r.GetVOD(ctx, *s.VODID)
	require.NoError(t, err)
	require.Equal(t, 42, v.DurationSec)
	require.Equal(t, "Rank game 2", v.Title)

	// Next publish starts a new stream.
	g3, err := r.Authorize(ctx, ch, grace)
	require.NoError(t, err)
	require.NotEqual(t, g.StreamID, g3.StreamID)

	// Creator ends it; ending again is NOT_LIVE.
	_, err = r.End(ctx, ch, entity.EndCreator)
	require.NoError(t, err)
	_, err = r.End(ctx, ch, entity.EndCreator)
	require.ErrorIs(t, err, entity.ErrNotLive)

	require.Equal(t, []string{
		events.TypeStreamStarted, events.TypeStreamEnded, events.TypeStreamStarted, events.TypeStreamEnded,
	}, outboxTypes(t, pg))

	var payload events.StreamEnded

	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT payload FROM outbox WHERE event_type = $1 ORDER BY created_at DESC LIMIT 1`, events.TypeStreamEnded).Scan(&payload))
	require.Equal(t, "creator", payload.EndReason)
	require.Equal(t, g3.StreamID, payload.StreamID)
}

func TestAuthorizeExpiredInterruptedStartsNew(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()

	g, err := r.Authorize(ctx, ch, time.Second)
	require.NoError(t, err)
	require.NoError(t, r.OnIngestStopped(ctx, "", g.StreamID))

	_, err = pg.Pool.Exec(ctx, `UPDATE streams SET interrupted_at = now() - interval '1 minute' WHERE id = $1`, g.StreamID)
	require.NoError(t, err)

	g2, err := r.Authorize(ctx, ch, time.Second)
	require.NoError(t, err)
	require.False(t, g2.Resumed)
	require.NotEqual(t, g.StreamID, g2.StreamID)

	old, err := r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusEnded, old.Status)
}

func TestVODBeforeEndEndsStream(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()

	g, err := r.Authorize(ctx, uuid.NewString(), time.Minute)
	require.NoError(t, err)

	// media-svc shut down and finalized first.
	require.NoError(t, r.OnVODProcessed(ctx, uuid.NewString(), repo.VODInput{StreamID: g.StreamID, DurationSec: 3, PlaylistPath: "streams/x/master.m3u8", Retention: time.Hour}))

	s, err := r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.Equal(t, entity.StatusEnded, s.Status)
	require.NotNil(t, s.VODID)
}

func TestListOnAirCursor(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()

	for range 5 {
		_, err := r.Authorize(ctx, uuid.NewString(), time.Minute)
		require.NoError(t, err)
	}

	page1, err := r.ListOnAir(ctx, nil, 3)
	require.NoError(t, err)
	require.Len(t, page1, 3)

	last := page1[2]
	page2, err := r.ListOnAir(ctx, &repo.Cursor{StartedAt: *last.StartedAt, ID: last.ID}, 3)
	require.NoError(t, err)
	require.Len(t, page2, 2)

	for _, a := range page1 {
		for _, b := range page2 {
			require.NotEqual(t, a.ID, b.ID)
		}
	}
}

func TestEndedByCreatorSince(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	creator, timeout := uuid.NewString(), uuid.NewString()

	recent, err := r.EndedByCreatorSince(ctx, creator, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.False(t, recent, "a channel that never streamed")

	for _, ch := range []string{creator, timeout} {
		_, err = r.Authorize(ctx, ch, time.Minute)
		require.NoError(t, err)
	}

	_, err = r.End(ctx, creator, entity.EndCreator)
	require.NoError(t, err)
	_, err = r.End(ctx, timeout, entity.EndIngestTimeout)
	require.NoError(t, err)

	recent, err = r.EndedByCreatorSince(ctx, creator, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.True(t, recent, "ended by the creator inside the window")

	recent, err = r.EndedByCreatorSince(ctx, creator, time.Now().Add(time.Second))
	require.NoError(t, err)
	require.False(t, recent, "outside the window")

	recent, err = r.EndedByCreatorSince(ctx, timeout, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.False(t, recent, "an ingest timeout does not start a cooldown")
}

func TestExpireVODs(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ch := uuid.NewString()

	g, err := r.Authorize(ctx, ch, time.Minute)
	require.NoError(t, err)
	require.NoError(t, r.OnVODProcessed(ctx, "", repo.VODInput{StreamID: g.StreamID, DurationSec: 60, Retention: time.Hour}))

	s, err := r.Get(ctx, g.StreamID)
	require.NoError(t, err)
	require.NotNil(t, s.VODID)

	n, err := r.ExpireVODs(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "still inside retention")

	_, err = pg.Pool.Exec(ctx, `UPDATE vods SET expires_at = now() - interval '1 second' WHERE id = $1`, *s.VODID)
	require.NoError(t, err)

	n, err = r.ExpireVODs(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	_, err = r.GetVOD(ctx, *s.VODID)
	require.ErrorIs(t, err, entity.ErrNotFound, "expired VODs are gone for viewers")

	onAir, err := r.CountOnAir(ctx)
	require.NoError(t, err)
	require.Zero(t, onAir)
}

func TestCreatorChannels(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	owner, chID, ev := uuid.NewString(), uuid.NewString(), uuid.NewString()

	_, err := r.ChannelOfOwner(ctx, owner)
	require.ErrorIs(t, err, entity.ErrNotFound)

	c := entity.Channel{ID: chID, OwnerID: owner, Handle: "minhnn", DisplayName: "Minh"}
	require.NoError(t, r.UpsertChannel(ctx, ev, c))
	// Redelivery is ignored; a later event for the same owner updates the copy.
	require.NoError(t, r.UpsertChannel(ctx, ev, entity.Channel{ID: chID, OwnerID: owner, Handle: "ignored", DisplayName: "x"}))

	got, err := r.ChannelOfOwner(ctx, owner)
	require.NoError(t, err)
	require.Equal(t, c, got)

	require.NoError(t, r.UpsertChannel(ctx, uuid.NewString(), entity.Channel{ID: chID, OwnerID: owner, Handle: "minhnn2", DisplayName: "Minh 2"}))

	got, err = r.ChannelOfOwner(ctx, owner)
	require.NoError(t, err)
	require.Equal(t, "minhnn2", got.Handle)

	byID, err := r.ChannelsByID(ctx, []string{chID, uuid.NewString(), chID})
	require.NoError(t, err)
	require.Len(t, byID, 1)
	require.Equal(t, owner, byID[chID].OwnerID)

	empty, err := r.ChannelsByID(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

// insertVOD stores an ENDED stream and its READY VOD of durationSec; returns the VOD.
func insertVOD(t *testing.T, pg *postgres.Postgres, channelID string, durationSec int, expiresAt time.Time) entity.VOD {
	t.Helper()

	ctx := context.Background()
	streamID, vodID := uuid.NewString(), uuid.NewString()

	_, err := pg.Pool.Exec(ctx,
		`INSERT INTO streams (id, channel_id, title, status, started_at, ended_at, end_reason)
		 VALUES ($1, $2, 't', 'ENDED', now() - interval '1 hour', now(), 'creator')`, streamID, channelID)
	require.NoError(t, err)

	_, err = pg.Pool.Exec(ctx,
		`INSERT INTO vods (id, stream_id, channel_id, title, duration_sec, playlist_path, status, expires_at)
		 VALUES ($1, $2, $3, 't', $4, 'streams/x/vod.m3u8', 'READY', $5)`, vodID, streamID, channelID, durationSec, expiresAt)
	require.NoError(t, err)

	return entity.VOD{ID: vodID, StreamID: streamID, ChannelID: channelID, DurationSec: durationSec}
}

func TestViewer(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	user, channel := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)

	// Mature consent is idempotent and per channel.
	ok, err := r.HasMatureConsent(ctx, user, channel)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, r.SaveMatureConsent(ctx, user, channel))
	require.NoError(t, r.SaveMatureConsent(ctx, user, channel))
	ok, err = r.HasMatureConsent(ctx, user, channel)
	require.NoError(t, err)
	require.True(t, ok)

	// Sessions for a signed-in user and a guest, then purge of the expired one.
	watched := insertVOD(t, pg, channel, 600, now.Add(24*time.Hour))
	require.NoError(t, r.CreatePlaybackSession(ctx, entity.PlaybackSession{
		ID: uuid.NewString(), Target: entity.PlaybackTarget{VODID: watched.ID}, UserID: user,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}))
	require.NoError(t, r.CreatePlaybackSession(ctx, entity.PlaybackSession{
		ID: uuid.NewString(), Target: entity.PlaybackTarget{StreamID: watched.StreamID}, AnonID: uuid.NewString(),
		MatureAcknowledged: true, CreatedAt: now.Add(-6 * time.Hour), ExpiresAt: now.Add(-2 * time.Hour),
	}))
	n, err := r.PurgePlaybackSessions(ctx, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	// Progress: keepFurther keeps the larger value, a plain save overwrites.
	pos, err := r.Progress(ctx, user, watched.ID)
	require.NoError(t, err)
	require.Nil(t, pos)

	save := func(p int, keep bool, at time.Time) {
		require.NoError(t, r.SaveProgress(ctx, entity.WatchProgress{UserID: user, VODID: watched.ID, PositionSec: p, UpdatedAt: at}, keep))
	}

	save(300, false, now)
	save(100, true, now.Add(time.Second))

	pos, err = r.Progress(ctx, user, watched.ID)
	require.NoError(t, err)
	require.Equal(t, 300, *pos)

	save(120, false, now.Add(2*time.Second))

	pos, err = r.Progress(ctx, user, watched.ID)
	require.NoError(t, err)
	require.Equal(t, 120, *pos)

	// Continue watching skips barely started, finished and expired VODs; newest first.
	finished := insertVOD(t, pg, channel, 600, now.Add(24*time.Hour))
	barely := insertVOD(t, pg, channel, 600, now.Add(24*time.Hour))
	expired := insertVOD(t, pg, channel, 600, now.Add(-time.Minute))
	later := insertVOD(t, pg, channel, 900, now.Add(24*time.Hour))

	for _, p := range []entity.WatchProgress{
		{VODID: finished.ID, PositionSec: 595},
		{VODID: barely.ID, PositionSec: 3},
		{VODID: expired.ID, PositionSec: 50},
		{VODID: later.ID, PositionSec: 60, UpdatedAt: now.Add(time.Minute)},
	} {
		if p.UpdatedAt.IsZero() {
			p.UpdatedAt = now
		}

		p.UserID = user
		require.NoError(t, r.SaveProgress(ctx, p, false))
	}

	items, err := r.ContinueWatching(ctx, user, 10)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, later.ID, items[0].VOD.ID)
	require.Equal(t, 60, items[0].PositionSec)
	require.Equal(t, 900, items[0].VOD.DurationSec)
	require.Equal(t, watched.ID, items[1].VOD.ID)

	items, err = r.ContinueWatching(ctx, user, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)

	items, err = r.ContinueWatching(ctx, uuid.NewString(), 10)
	require.NoError(t, err)
	require.Empty(t, items)
}
