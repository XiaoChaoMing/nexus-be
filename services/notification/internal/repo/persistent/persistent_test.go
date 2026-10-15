package persistent_test

// Integration tests against a migrated nxl_notification database:
//   NXL_TEST_PG_URL_NOTIFICATION=postgres://user:pass@localhost:5432/nxl_notification?sslmode=disable go test ./...
// Skipped when the variable is not set.

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

// The tests share one database: they run one after the other and truncate first.
var dbMu sync.Mutex //nolint:gochecknoglobals // serializes the tests on the shared database

func setup(t *testing.T) (*persistent.Repo, *postgres.Postgres) {
	t.Helper()

	url := os.Getenv("NXL_TEST_PG_URL_NOTIFICATION")
	if url == "" {
		t.Skip("NXL_TEST_PG_URL_NOTIFICATION not set")
	}

	dbMu.Lock()
	t.Cleanup(dbMu.Unlock)

	pg, err := postgres.New(url, postgres.MaxPoolSize(8))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	_, err = pg.Pool.Exec(context.Background(), `TRUNCATE notifications, notification_settings, email_outbox, outbox,
		processed_events, users, channels, follows, user_blocks, replica_backfills`)
	require.NoError(t, err)

	return persistent.New(pg), pg
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

type outboxRow struct {
	typ     string
	payload map[string]any
}

func outbox(t *testing.T, pg *postgres.Postgres) []outboxRow {
	t.Helper()

	rows, err := pg.Pool.Query(context.Background(), `SELECT event_type, payload FROM outbox ORDER BY created_at, id`)
	require.NoError(t, err)

	defer rows.Close()

	var out []outboxRow

	for rows.Next() {
		var r outboxRow
		require.NoError(t, rows.Scan(&r.typ, &r.payload))
		out = append(out, r)
	}

	return out
}

func live() entity.Notification {
	return entity.Notification{
		Type: entity.TypeCreatorLive, Payload: json.RawMessage(`{"title":"t"}`), Link: ptr("/live/x"),
	}
}

func ptr[T any](v T) *T { return &v }

func email(userID string) entity.Email {
	return entity.Email{UserID: userID, To: "a@x.dev", Template: "live_started", Locale: "vi", Data: map[string]string{"Title": "t"}}
}

func TestInsertBatchIsIdempotent(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	ev, u1, u2, u3 := newID(), newID(), newID(), newID()

	res, err := r.InsertBatch(ctx, ev, live(), []string{u1, u2}, []entity.Email{email(u1), email(u3)})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{u1, u2}, res.Notified)
	require.ElementsMatch(t, []string{u1, u3}, res.Emailed)

	// A redelivery writes nothing.
	res, err = r.InsertBatch(ctx, ev, live(), []string{u1, u2}, []entity.Email{email(u1), email(u3)})
	require.NoError(t, err)
	require.Empty(t, res.Notified)
	require.Empty(t, res.Emailed)

	ob := outbox(t, pg)
	require.Len(t, ob, 2)
	require.Equal(t, events.TypeNotificationCreated, ob[0].typ)
	require.InDelta(t, 1, ob[0].payload["unreadCount"], 0)

	var linked, unlinked int
	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT count(notification_id), count(*) - count(notification_id)
		FROM email_outbox`).Scan(&linked, &unlinked))
	require.Equal(t, [2]int{1, 1}, [2]int{linked, unlinked}, "u1 e-mail linked to its row; u3 has e-mail only")

	n, err := r.UnreadCount(ctx, u1)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func mention(userID string) repo.Pending {
	return repo.Pending{
		Notification: entity.Notification{
			UserID: userID, Type: entity.TypeMention, Payload: json.RawMessage(`{"excerpt":"hi"}`), Link: ptr("/live/s"),
			GroupKey: ptr("MENTION:s"),
		},
		Window: 5 * time.Minute,
		Email:  &entity.Email{UserID: userID, To: "a@x.dev", Template: "mention", Locale: "vi", Data: map[string]string{}},
	}
}

func TestUpsertGroups(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	u := newID()

	first, err := r.Upsert(ctx, newID(), []repo.Pending{mention(u)})
	require.NoError(t, err)
	require.False(t, first[0].Grouped)
	require.True(t, first[0].Emailed)

	require.NoError(t, r.MarkRead(ctx, u, first[0].Notification.ID))

	ev := newID()
	second, err := r.Upsert(ctx, ev, []repo.Pending{mention(u)})
	require.NoError(t, err)
	require.True(t, second[0].Grouped)
	require.False(t, second[0].Emailed, "no second e-mail when grouped")
	require.Equal(t, first[0].Notification.ID, second[0].Notification.ID)
	require.Equal(t, 2, second[0].Notification.GroupCount)
	require.False(t, second[0].Notification.Read(), "grouping marks it unread again (AF-E06)")

	// The same event again is a no-op.
	again, err := r.Upsert(ctx, ev, []repo.Pending{mention(u)})
	require.NoError(t, err)
	require.Nil(t, again)

	// Outside the window: a new row.
	_, err = pg.Pool.Exec(ctx, `UPDATE notifications SET updated_at = now() - interval '6 minutes'`)
	require.NoError(t, err)

	third, err := r.Upsert(ctx, newID(), []repo.Pending{mention(u)})
	require.NoError(t, err)
	require.False(t, third[0].Grouped)
	require.NotEqual(t, first[0].Notification.ID, third[0].Notification.ID)

	created := 0

	for _, o := range outbox(t, pg) {
		if o.typ == events.TypeNotificationCreated {
			created++
		}
	}

	require.Equal(t, 3, created, "every create and group is announced")
}

func TestUpsertConcurrentGroupsIntoOneRow(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	u := newID()

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			_, err := r.Upsert(ctx, newID(), []repo.Pending{mention(u)})
			require.NoError(t, err)
		})
	}

	wg.Wait()

	var rows, count int
	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT count(*), max(group_count) FROM notifications`).Scan(&rows, &count))
	require.Equal(t, [2]int{1, 8}, [2]int{rows, count})
}

func TestReadAndDelete(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	u, other := newID(), newID()

	_, err := r.InsertBatch(ctx, newID(), live(), []string{u, other}, nil)
	require.NoError(t, err)
	_, err = r.InsertBatch(ctx, newID(), live(), []string{u}, nil)
	require.NoError(t, err)

	list, err := r.List(ctx, u, false, nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)

	require.ErrorIs(t, r.MarkRead(ctx, other, list[0].ID), entity.ErrNotFound, "another user's")
	require.NoError(t, r.MarkRead(ctx, u, list[0].ID))
	require.NoError(t, r.MarkRead(ctx, u, list[0].ID), "already read")

	unread, err := r.List(ctx, u, true, nil, 10)
	require.NoError(t, err)
	require.Len(t, unread, 1)

	page, err := r.List(ctx, u, false, &repo.Cursor{UpdatedAt: list[0].UpdatedAt, ID: list[0].ID}, 10)
	require.NoError(t, err)
	require.Equal(t, []string{list[1].ID}, []string{page[0].ID})

	n, err := r.MarkAllRead(ctx, u, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Zero(t, n, "before excludes newer notifications (BR-E19)")

	n, err = r.MarkAllRead(ctx, u, time.Now())
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	require.NoError(t, r.SoftDelete(ctx, u, list[1].ID))
	require.NoError(t, r.SoftDelete(ctx, u, list[1].ID), "already deleted")
	require.ErrorIs(t, r.SoftDelete(ctx, other, list[0].ID), entity.ErrNotFound)
	require.ErrorIs(t, r.MarkRead(ctx, u, list[1].ID), entity.ErrNotFound, "deleted")

	var changes []float64

	for _, o := range outbox(t, pg) {
		if o.typ == events.TypeNotificationUnreadChanged && o.payload["userId"] == u {
			n, ok := o.payload["unreadCount"].(float64)
			require.True(t, ok)

			changes = append(changes, n)
		}
	}

	require.Equal(t, []float64{1, 0}, changes, "read one, read all; deleting a read row changes nothing")
}

func TestReplicaAndRecipients(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, _ := setup(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	owner, a, b, locked, off := newID(), newID(), newID(), newID(), newID()
	ch := newID()

	for _, id := range []string{owner, a, b, locked, off} {
		require.NoError(t, r.PatchUser(ctx, t0, entity.UserCopy{
			ID: id, Username: ptr("u" + id[30:]), Email: ptr(id[30:] + "@x.dev"), Locale: ptr(""), Timezone: ptr(""),
		}))
	}

	require.NoError(t, r.PatchUser(ctx, t0.Add(time.Minute), entity.UserCopy{ID: a, EmailVerified: ptr(true)}))
	require.NoError(t, r.PatchUser(ctx, t0.Add(time.Minute), entity.UserCopy{ID: locked, Status: ptr(entity.UserLocked)}))
	// An older event does not overwrite a newer copy.
	require.NoError(t, r.PatchUser(ctx, t0.Add(-time.Minute), entity.UserCopy{ID: a, DisplayName: ptr("old")}))

	require.NoError(t, r.PatchChannel(ctx, t0, entity.ChannelPatch{ID: ch, OwnerID: &owner, Handle: ptr("k"), DisplayName: ptr("K")}))

	for id, level := range map[string]string{owner: "ALL", a: "ALL", b: "LIVE_ONLY", locked: "ALL", off: "OFF"} {
		require.NoError(t, r.SetFollow(ctx, t0, entity.Follow{UserID: id, ChannelID: ch, NotifyLevel: level}))
	}

	live, err := r.Followers(ctx, ch, []string{"ALL", "LIVE_ONLY"}, owner, "", 500)
	require.NoError(t, err)

	got := []string{}
	for _, f := range live {
		got = append(got, f.UserID)
	}

	require.ElementsMatch(t, []string{a, b}, got, "OFF, the owner and locked accounts are left out")

	all, err := r.Followers(ctx, ch, []string{"ALL"}, owner, "", 500)
	require.NoError(t, err)
	require.Len(t, all, 1)

	page, err := r.Followers(ctx, ch, []string{"ALL", "LIVE_ONLY"}, owner, "", 1)
	require.NoError(t, err)
	next, err := r.Followers(ctx, ch, []string{"ALL", "LIVE_ONLY"}, owner, page[0].UserID, 1)
	require.NoError(t, err)
	require.NotEqual(t, page[0].UserID, next[0].UserID)

	rs, err := r.Recipients(ctx, []string{a, "bad", newID()})
	require.NoError(t, err)
	require.Len(t, rs, 1)
	require.True(t, rs[a].EmailVerified)
	require.Equal(t, "vi", rs[a].Locale)
	require.Equal(t, entity.DefaultTimezone, rs[a].Timezone)
	require.NotEqual(t, "old", rs[a].DisplayName)

	require.NoError(t, r.SetBlock(ctx, a, b, true))
	blocked, err := r.BlockedBy(ctx, b, []string{a, owner})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{a: true}, blocked)

	require.NoError(t, r.RemoveFollow(ctx, t0.Add(-time.Minute), a, ch))
	require.Contains(t, followerIDs(t, r, ch), a, "an older unfollow is ignored")
	require.NoError(t, r.RemoveFollow(ctx, t0.Add(time.Minute), a, ch))
	require.NotContains(t, followerIDs(t, r, ch), a)

	_, err = r.Channel(ctx, newID())
	require.ErrorIs(t, err, entity.ErrNotFound)
}

func followerIDs(t *testing.T, r *persistent.Repo, ch string) []string {
	t.Helper()

	out, err := r.Followers(context.Background(), ch, []string{"ALL", "LIVE_ONLY", "OFF"}, newID(), "", 500)
	require.NoError(t, err)

	ids := make([]string, 0, len(out))
	for _, f := range out {
		ids = append(ids, f.UserID)
	}

	return ids
}

func TestSettingsSaveDropsDefaults(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	u := newID()

	require.NoError(t, r.Save(ctx, u, []entity.Setting{{Type: entity.TypeCreatorLive, InApp: true, Email: false}}))

	o, err := r.Overrides(ctx, []string{u})
	require.NoError(t, err)
	require.Equal(t, entity.Setting{Type: entity.TypeCreatorLive, InApp: true}, o[u][entity.TypeCreatorLive])

	require.NoError(t, r.Save(ctx, u, []entity.Setting{{Type: entity.TypeCreatorLive, InApp: true, Email: true}}))

	o, err = r.Overrides(ctx, []string{u})
	require.NoError(t, err)
	require.Empty(t, o)
}

func TestEmailWorkerRows(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()

	for range 3 {
		_, err := r.QueueEmail(ctx, newID(), email(newID()))
		require.NoError(t, err)
	}

	// Two workers lease disjoint rows (FOR UPDATE SKIP LOCKED + lease).
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]int{}
	)

	for range 2 {
		wg.Go(func() {
			due, err := r.Due(ctx, 2, time.Minute)
			require.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()

			for _, e := range due {
				seen[e.ID]++
			}
		})
	}

	wg.Wait()
	require.Len(t, seen, 3)

	for id, n := range seen {
		require.Equal(t, 1, n, id)
	}

	due, err := r.Due(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Empty(t, due, "leased rows are not offered again")

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}

	next := time.Now().Add(-time.Second)
	require.NoError(t, r.MarkFailed(ctx, ids[0], 1, &next, "smtp down"))
	require.NoError(t, r.MarkFailed(ctx, ids[1], 5, nil, "smtp down"))
	require.NoError(t, r.MarkSent(ctx, ids[2]))

	due, err = r.Due(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, ids[0], due[0].ID)
	require.Equal(t, 1, due[0].Attempts)
	require.Equal(t, "t", due[0].Data["Title"])

	var failed, sent int
	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'FAILED'),
		count(*) FILTER (WHERE status = 'SENT') FROM email_outbox`).Scan(&failed, &sent))
	require.Equal(t, [2]int{1, 1}, [2]int{failed, sent})

	n, err := r.PurgeDone(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}

func TestPurge(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	u := newID()

	_, err := r.InsertBatch(ctx, newID(), live(), []string{u, newID(), newID()}, nil)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `UPDATE notifications SET created_at = now() - interval '91 days' WHERE user_id <> $1`, u)
	require.NoError(t, err)

	n, err := r.Purge(ctx, time.Now().Add(-90*24*time.Hour), 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}
