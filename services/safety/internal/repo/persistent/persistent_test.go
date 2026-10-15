package persistent_test

// Integration tests against a migrated nxl_safety database:
//   NXL_TEST_PG_URL_SAFETY=postgres://user:pass@localhost:5432/nxl_safety?sslmode=disable go test ./...
// Skipped when the variable is not set.

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) (*persistent.Repo, *postgres.Postgres) {
	t.Helper()

	url := os.Getenv("NXL_TEST_PG_URL_SAFETY")
	if url == "" {
		t.Skip("NXL_TEST_PG_URL_SAFETY not set")
	}

	pg, err := postgres.New(url, postgres.MaxPoolSize(4))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	_, err = pg.Pool.Exec(context.Background(), `TRUNCATE reports, admin_actions, audit_logs, outbox, processed_events,
		users, channels, streams, replica_backfills CASCADE`)
	require.NoError(t, err)

	return persistent.New(pg), pg
}

func outbox(t *testing.T, pg *postgres.Postgres) []string {
	t.Helper()

	rows, err := pg.Pool.Query(context.Background(), `SELECT event_type FROM outbox ORDER BY created_at, id`)
	require.NoError(t, err)

	defer rows.Close()

	out := []string{}

	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}

	return out
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

func str(s string) *string { return &s }

func TestReportLifecycle(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	reporter, other, target, admin := newID(), newID(), newID(), newID()
	stream := newID()

	in := entity.Report{
		ReporterID: reporter, TargetType: entity.TargetMessage, TargetID: target, TargetUserID: str(other), StreamID: &stream,
		Reason: entity.ReasonSpam, EvidenceStatus: entity.EvidenceCaptured,
		Evidence: []entity.EvidenceItem{{MessageID: target, UserID: other, Body: "spam", Fragments: json.RawMessage(`[]`)}},
	}

	first, created, err := r.Insert(ctx, in)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, entity.ReportOpen, first.Status)
	require.Len(t, first.Evidence, 1)

	// BR-F15: the second report of the same target returns the open one.
	dup, created, err := r.Insert(ctx, in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, dup.ID)

	found, err := r.OpenDuplicate(ctx, reporter, entity.TargetMessage, target)
	require.NoError(t, err)
	require.Equal(t, first.ID, found.ID)

	in.ReporterID = newID()
	second, _, err := r.Insert(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 2, second.SameTarget)

	open, inReview, err := r.Counts(ctx)
	require.NoError(t, err)
	require.Equal(t, [2]int{2, 0}, [2]int{open, inReview})

	claimed, err := r.Claim(ctx, admin, first.ID)
	require.NoError(t, err)
	require.Equal(t, entity.ReportInReview, claimed.Status)
	require.Equal(t, admin, claimed.AssignedTo.ID)

	list, err := r.List(ctx, repo.ReportFilter{Statuses: []string{entity.ReportInReview}}, nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)

	related, err := r.Related(ctx, first, 20)
	require.NoError(t, err)
	require.Len(t, related, 1)

	resolved, closed, err := r.Resolve(ctx, admin, first.ID, repo.Resolution{
		Status: entity.ReportResolved, Note: "handled the spam", ApplyToSameTarget: true,
	})
	require.NoError(t, err)
	require.Equal(t, 2, closed)
	require.Equal(t, entity.ReportResolved, resolved.Status)
	require.NotNil(t, resolved.ResolvedAt)
	require.Equal(t, []string{events.TypeReportResolved, events.TypeReportResolved}, outbox(t, pg))

	_, _, err = r.Resolve(ctx, admin, first.ID, repo.Resolution{Status: entity.ReportDismissed, Note: "again and again"})
	require.ErrorIs(t, err, entity.ErrInvalidState)
	_, err = r.Claim(ctx, admin, first.ID)
	require.ErrorIs(t, err, entity.ErrInvalidState)
	_, err = r.Claim(ctx, admin, newID())
	require.ErrorIs(t, err, entity.ErrNotFound)

	// A closed report no longer blocks a new one.
	_, created, err = r.Insert(ctx, in)
	require.NoError(t, err)
	require.True(t, created)

	audit, err := persistent.Audit{Repo: r}.List(ctx, repo.AuditFilter{ResourceType: str("REPORT")}, nil, 10)
	require.NoError(t, err)
	require.Len(t, audit, 3)
	require.Equal(t, "REPORT_RESOLVED", audit[0].Action)
	require.Equal(t, events.ActorAdmin, audit[0].ActorRole)
	require.Equal(t, "handled the spam", *audit[0].Reason)
}

func TestActionsAndAudit(t *testing.T) { //nolint:paralleltest // shared database
	r, pg := setup(t)
	ctx := context.Background()
	admin, user := newID(), newID()
	audit := persistent.Audit{Repo: r}

	id, err := r.Record(ctx, entity.AdminAction{
		AdminID: admin, Type: entity.ActionLockUser, TargetType: entity.TargetUser, TargetID: user, Reason: "spamming chat rooms",
	}, entity.AuditEntry{
		ActorID: &admin, ActorRole: events.ActorAdmin, Action: "USER_LOCKED", ResourceType: entity.TargetUser, ResourceID: &user,
		Reason: str("spamming chat rooms"),
	}, events.TypeUserActioned, events.UserActioned{UserID: user, Action: events.ActionLock, AdminID: admin})
	require.NoError(t, err)

	var payload events.UserActioned

	require.NoError(t, pg.Pool.QueryRow(ctx, `SELECT payload FROM outbox`).Scan(&payload))
	require.Equal(t, id, payload.ActionID)

	acts, err := r.ForReport(ctx, entity.Report{TargetType: entity.TargetMessage, TargetID: newID(), TargetUserID: &user}, 20)
	require.NoError(t, err)
	require.Len(t, acts, 1)

	// Redelivered events add one row only.
	eventID := newID()
	entry := entity.AuditEntry{
		ActorRole: events.ActorSystem, Action: "ADMIN_PROMOTED", ResourceType: entity.TargetUser,
		ResourceID: &user, SourceEventID: &eventID, SourceService: "identity-svc",
	}
	require.NoError(t, r.PatchUser(ctx, "", time.Now(), repo.UserPatch{ID: user, Username: str("promoted")}))
	require.NoError(t, audit.Insert(ctx, entry, &user))
	require.NoError(t, audit.Insert(ctx, entry, &user))

	rows, err := audit.List(ctx, repo.AuditFilter{ResourceID: &user}, nil, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, id, rows[1].Metadata["actionId"])

	u, err := r.User(ctx, user)
	require.NoError(t, err)
	require.Equal(t, entity.RoleAdmin, u.GlobalRole)

	// NFR-BE-07: append-only.
	_, err = pg.Pool.Exec(ctx, `UPDATE audit_logs SET action = 'X'`)
	require.ErrorContains(t, err, "append-only")
	_, err = pg.Pool.Exec(ctx, `DELETE FROM audit_logs`)
	require.ErrorContains(t, err, "append-only")
}

func TestReplica(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, _ := setup(t)
	ctx := context.Background()
	user, owner, channel, stream, vod := newID(), newID(), newID(), newID(), newID()
	t0 := time.Now().Add(-time.Hour)

	require.NoError(t, r.PatchUser(ctx, newID(), t0, repo.UserPatch{ID: user, Username: str("Annie"), DisplayName: str("Annie")}))
	require.NoError(t, r.PatchUser(ctx, newID(), t0, repo.UserPatch{ID: owner, Username: str("bob"), DisplayName: str("Bob")}))

	until := time.Now().Add(-time.Minute) // an expired lock
	require.NoError(t, r.PatchUser(ctx, newID(), t0.Add(time.Minute), repo.UserPatch{
		ID: user, Status: str(entity.UserLocked),
		LockedUntil: &until,
	}))
	// An older replay does not overwrite the lock.
	require.NoError(t, r.PatchUser(ctx, newID(), t0, repo.UserPatch{ID: user, Status: str(entity.UserActive)}))

	u, err := r.User(ctx, user)
	require.NoError(t, err)
	require.Equal(t, entity.UserLocked, u.Status)

	users, err := r.SearchUsers(ctx, "ann", str(entity.UserActive), nil, 10)
	require.NoError(t, err)
	require.Len(t, users, 1, "an expired lock reads as ACTIVE")

	users, err = r.SearchUsers(ctx, "", nil, &repo.NameCursor{Name: "annie", ID: user}, 10)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, owner, users[0].ID)

	require.NoError(t, r.PatchChannel(ctx, newID(), t0, repo.ChannelPatch{
		ID: channel, OwnerID: &owner, Handle: str("bobtv"),
		DisplayName: str("Bob TV"),
	}))
	require.NoError(t, r.PatchChannel(ctx, newID(), t0.Add(time.Minute), repo.ChannelPatch{
		ID:     channel,
		Status: str(entity.ChannelSuspended), SuspendedAt: &t0,
	}))
	require.NoError(t, r.PatchStream(ctx, newID(), t0, repo.StreamPatch{
		ID: stream, ChannelID: channel, Title: str("Live!"),
		Status: str(entity.StreamLive), StartedAt: &t0,
	}))

	c, err := r.Channel(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, entity.ChannelSuspended, c.Status)
	require.Equal(t, "bob", c.Owner.Username)
	require.Equal(t, &stream, c.CurrentStreamID)

	chans, err := r.SearchChannels(ctx, "TV", nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, chans, 1)

	require.NoError(t, r.PatchStream(ctx, newID(), t0.Add(time.Minute), repo.StreamPatch{
		ID: stream, ChannelID: channel,
		Status: str(entity.StreamEnded), EndReason: str("creator_ended"), EndedAt: &t0,
	}))
	require.NoError(t, r.PatchStream(ctx, newID(), t0.Add(2*time.Minute), repo.StreamPatch{ID: stream, ChannelID: channel, VODID: &vod}))

	s, err := r.Stream(ctx, stream)
	require.NoError(t, err)
	require.True(t, s.TakedownAllowed())

	require.NoError(t, r.PatchStream(ctx, newID(), t0.Add(3*time.Minute), repo.StreamPatch{
		ID: stream, ChannelID: channel, VODID: &vod,
		VODDeleted: true,
	}))

	s, err = r.Stream(ctx, stream)
	require.NoError(t, err)
	require.False(t, s.TakedownAllowed())
	require.Equal(t, "Live!", s.Title)

	l, err := r.Lookup(ctx, repo.LookupIDs{Users: []string{user, "nope"}, Channels: []string{channel}, Streams: []string{stream}})
	require.NoError(t, err)
	require.Len(t, l.Users, 1)
	require.Len(t, l.Channels, 1)
	require.Len(t, l.Streams, 1)

	done, err := r.BackfillDone(ctx, "users")
	require.NoError(t, err)
	require.False(t, done)
	require.NoError(t, r.MarkBackfilled(ctx, "users"))
	done, err = r.BackfillDone(ctx, "users")
	require.NoError(t, err)
	require.True(t, done)

	n, err := r.PurgeProcessed(ctx, time.Hour)
	require.NoError(t, err)
	require.Zero(t, n)
}
