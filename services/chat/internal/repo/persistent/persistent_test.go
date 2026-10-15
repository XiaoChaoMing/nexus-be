package persistent_test

// Integration tests against a migrated nxl_chat database:
//   NXL_TEST_PG_URL_CHAT=postgres://user:pass@localhost:5432/nxl_chat?sslmode=disable go test ./...
// Skipped when the variable is not set.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) (*persistent.Repo, *postgres.Postgres) {
	t.Helper()

	url := os.Getenv("NXL_TEST_PG_URL_CHAT")
	if url == "" {
		t.Skip("NXL_TEST_PG_URL_CHAT not set")
	}

	pg, err := postgres.New(url, postgres.MaxPoolSize(4))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	_, err = pg.Pool.Exec(context.Background(),
		`TRUNCATE chat_messages, chat_rooms, chat_settings, channel_roles, mod_actions, blocked_terms, users, channels, outbox,
		 processed_events, replica_backfills CASCADE`)
	require.NoError(t, err)

	return persistent.New(pg), pg
}

func TestRoomLifecycleAndSendContext(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	stream, channel, owner, user := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()

	_, err := r.SendContext(ctx, stream, user)
	require.ErrorIs(t, err, entity.ErrNotFound)

	opened, err := r.OpenRoom(ctx, entity.Room{StreamID: stream, ChannelID: channel})
	require.NoError(t, err)
	require.True(t, opened)

	opened, err = r.OpenRoom(ctx, entity.Room{StreamID: stream, ChannelID: channel})
	require.NoError(t, err)
	require.False(t, opened, "redelivered stream.started")

	// Defaults: no owner, no settings, no profile; guests too.
	sc, err := r.SendContext(ctx, stream, "")
	require.NoError(t, err)
	require.Equal(t, entity.RoomOpen, sc.Room.Status)
	require.Equal(t, channel, sc.Room.ChannelID)
	require.Empty(t, sc.OwnerID)
	require.Nil(t, sc.Settings.UpdatedAt)
	require.Nil(t, sc.User)

	require.NoError(t, r.UpsertChannel(ctx, uuid.NewString(), entity.Channel{ID: channel, OwnerID: owner}))
	require.NoError(t, r.UpsertUser(ctx, uuid.NewString(), entity.User{ID: user, Username: "Viewer_A", DisplayName: "A"}))
	require.NoError(t, r.MarkEmailVerified(ctx, uuid.NewString(), user))
	require.NoError(t, r.GrantRole(ctx, channel, user, entity.ChannelRoleVIP, owner))

	ten := 10
	_, err = r.SaveSettings(ctx, entity.ChatSettings{ChannelID: channel, SlowSec: 5, FollowersMin: &ten, SubsOnly: true, UpdatedBy: &owner}, nil)
	require.NoError(t, err)

	_, err = pg.Pool.Exec(ctx,
		`INSERT INTO mod_actions (channel_id, actor_id, target_user_id, type, expires_at, created_at) VALUES
		 ($1, $2, $3, 'BAN', NULL, now() - interval '3 minutes'),
		 ($1, $2, $3, 'UNBAN', NULL, now() - interval '2 minutes'),
		 ($1, $2, $3, 'TIMEOUT', now() + interval '10 minutes', now() - interval '1 minute')`, channel, owner, user)
	require.NoError(t, err)

	sc, err = r.SendContext(ctx, stream, user)
	require.NoError(t, err)
	require.Equal(t, owner, sc.OwnerID)
	require.Equal(t, 5, sc.Settings.SlowSec)
	require.Equal(t, 10, *sc.Settings.FollowersMin)
	require.True(t, sc.Settings.SubsOnly)
	require.Equal(t, entity.ChannelRoleVIP, *sc.Role)
	require.Equal(t, &entity.User{ID: user, Username: "Viewer_A", DisplayName: "A", EmailVerified: true}, sc.User)
	require.False(t, sc.Restriction.Banned, "unbanned after the ban")
	require.NotNil(t, sc.Restriction.TimedOutUntil)

	rooms, err := r.OpenRoomsOfChannel(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, []string{stream}, rooms)

	closed, err := r.CloseRoom(ctx, stream, 7)
	require.NoError(t, err)
	require.True(t, closed)

	closed, err = r.CloseRoom(ctx, stream, 9)
	require.NoError(t, err)
	require.False(t, closed, "redelivered stream.ended")

	var payload events.StreamPeakViewers

	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT payload FROM outbox WHERE event_type = $1`, events.TypeStreamPeakViewers).Scan(&payload))
	require.Equal(t, events.StreamPeakViewers{StreamID: stream, PeakViewers: 7}, payload)

	sc, err = r.SendContext(ctx, stream, user)
	require.NoError(t, err)
	require.Equal(t, entity.RoomReadOnly, sc.Room.Status)

	ids, err := r.UserIDsByUsername(ctx, []string{"viewer_a", "nobody"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"viewer_a": user}, ids)

	roles, err := r.Roles(ctx, channel, []string{user, owner})
	require.NoError(t, err)
	require.Equal(t, map[string]entity.ChannelRole{user: entity.ChannelRoleVIP}, roles)

	require.NoError(t, r.RevokeRole(ctx, channel, user))
	roles, err = r.Roles(ctx, channel, []string{user})
	require.NoError(t, err)
	require.Empty(t, roles)
}

func TestMessagesHistoryAndPartitions(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	stream, user := uuid.NewString(), uuid.NewString()

	_, err := r.OpenRoom(ctx, entity.Room{StreamID: stream, ChannelID: uuid.NewString()})
	require.NoError(t, err)

	// A row written before today's partition exists lands in DEFAULT; creating the partition moves it.
	_, err = pg.Pool.Exec(ctx, `DROP TABLE IF EXISTS chat_messages_`+time.Now().UTC().Format("20060102"))
	require.NoError(t, err)

	frags := []entity.Fragment{{Type: "text", Text: "first"}}
	first, err := r.InsertMessage(ctx, entity.Message{
		StreamID: stream, User: entity.Author{ID: user}, Body: "first",
		Fragments: frags, Type: entity.MessageNormal, ClientMsgID: uuid.NewString(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, first.ID)

	created, err := r.EnsurePartitions(ctx, time.Now(), 2)
	require.NoError(t, err)
	require.Contains(t, created, "chat_messages_"+time.Now().UTC().Format("20060102"))

	again, err := r.EnsurePartitions(ctx, time.Now(), 2)
	require.NoError(t, err)
	require.Empty(t, again)

	var inDefault int

	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages_default WHERE stream_id = $1`, stream).Scan(&inDefault))
	require.Zero(t, inDefault)

	for i := range 4 {
		_, err = r.InsertMessage(ctx, entity.Message{
			StreamID: stream, User: entity.Author{ID: user}, Body: string(rune('a' + i)),
			Fragments: []entity.Fragment{}, Type: entity.MessageNormal, ClientMsgID: uuid.NewString(),
		})
		require.NoError(t, err)
	}

	require.NoError(t, r.UpsertUser(ctx, "", entity.User{ID: user, Username: "minhnn"}))

	page, err := r.History(ctx, stream, nil, 3)
	require.NoError(t, err)
	require.Len(t, page, 3)
	require.Equal(t, "d", page[0].Body)
	require.Equal(t, "minhnn", page[0].User.DisplayName, "display name falls back to the username")

	older, err := r.History(ctx, stream, &page[2].ID, 10)
	require.NoError(t, err)
	require.Len(t, older, 2)
	require.Equal(t, "first", older[1].Body)
	require.Equal(t, frags, older[1].Fragments)

	// Retention: an old empty partition is dropped, today's is kept.
	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	_, err = r.EnsurePartitions(ctx, old, 1)
	require.NoError(t, err)

	dropped, err := r.DropPartitionsBefore(ctx, time.Now().Add(-30*24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, []string{"chat_messages_" + old.Format("20060102")}, dropped)
}

func TestSettingsAndEmotes(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	channel := uuid.NewString()

	s, err := r.Settings(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, entity.ChatSettings{ChannelID: channel}, s)

	saved, err := r.SaveSettings(ctx, entity.ChatSettings{ChannelID: channel, SlowSec: 30}, nil)
	require.NoError(t, err)
	require.NotNil(t, saved.UpdatedAt)

	s, err = r.Settings(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, 30, s.SlowSec)
	require.Nil(t, s.FollowersMin)

	emotes, err := r.GlobalEmotes(ctx)
	require.NoError(t, err)
	require.Len(t, emotes, 12, "seeded by the migration")
	require.Equal(t, "/emotes/"+emotes[0].Code+".webp", emotes[0].ImageURL)

	_, err = r.Channel(ctx, channel)
	require.ErrorIs(t, err, entity.ErrNotFound)

	// processed_events makes a redelivered copy event a no-op.
	ev, user := uuid.NewString(), uuid.NewString()
	require.NoError(t, r.UpsertUser(ctx, ev, entity.User{ID: user, Username: "a1"}))
	require.NoError(t, r.UpsertUser(ctx, ev, entity.User{ID: user, Username: "a2"}))
	require.NoError(t, r.UpdateDisplayName(ctx, uuid.NewString(), user, "Shown"))

	users, err := r.Users(ctx, []string{user})
	require.NoError(t, err)
	require.Equal(t, entity.User{ID: user, Username: "a1", DisplayName: "Shown"}, users[user])
}
