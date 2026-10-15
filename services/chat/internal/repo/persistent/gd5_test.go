package persistent_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/persistent"
	"github.com/stretchr/testify/require"
)

type room struct {
	stream, channel, owner, mod, viewer string
}

func newRoom(t *testing.T, r *persistent.Repo) room {
	t.Helper()

	ctx := context.Background()
	x := room{stream: uuid.NewString(), channel: uuid.NewString(), owner: uuid.NewString(), mod: uuid.NewString(), viewer: uuid.NewString()}

	_, err := r.OpenRoom(ctx, entity.Room{StreamID: x.stream, ChannelID: x.channel})
	require.NoError(t, err)
	require.NoError(t, r.UpsertChannel(ctx, "", entity.Channel{ID: x.channel, OwnerID: x.owner, Handle: "minhnn", DisplayName: "Minh"}))

	for name, id := range map[string]string{"owner_x": x.owner, "mod_x": x.mod, "viewer_x": x.viewer} {
		require.NoError(t, r.UpsertUser(ctx, "", entity.User{ID: id, Username: name}))
	}

	return x
}

func send(t *testing.T, r *persistent.Repo, stream, user, body string) entity.Message {
	t.Helper()

	m, err := r.InsertMessage(context.Background(), entity.Message{
		StreamID: stream, User: entity.Author{ID: user}, ClientMsgID: uuid.NewString(), Body: body, Fragments: []entity.Fragment{},
		Type: entity.MessageNormal,
	})
	require.NoError(t, err)

	return m
}

func outboxCount(t *testing.T, pg *postgres.Postgres, typ string) int {
	t.Helper()

	var n int

	require.NoError(t, pg.Pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox WHERE event_type = $1`, typ).Scan(&n))

	return n
}

func action(x room, typ, target string) entity.ModAction {
	return entity.ModAction{ChannelID: x.channel, ActorID: x.mod, ActorRole: entity.ActorModerator, Type: typ, TargetUserID: &target}
}

func TestRestrictAndLift(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	x := newRoom(t, r)
	m := send(t, r, x.stream, x.viewer, "hello")

	until := time.Now().Add(time.Hour)
	timeout := action(x, entity.ModTimeout, x.viewer)
	timeout.ExpiresAt = &until

	rooms, unpinned, err := r.Restrict(ctx, timeout)
	require.NoError(t, err)
	require.Equal(t, []string{x.stream}, rooms)
	require.Empty(t, unpinned)

	got, err := r.Message(ctx, x.stream, m.ID)
	require.NoError(t, err)
	require.True(t, got.Deleted, "the timed out user's messages are cleared")

	sc, err := r.SendContext(ctx, x.stream, x.viewer)
	require.NoError(t, err)
	require.NotNil(t, sc.Restriction.TimedOutUntil)

	// A shorter timeout replaces a longer one (latest row wins).
	short := time.Now().Add(time.Minute)
	timeout.ExpiresAt = &short
	_, _, err = r.Restrict(ctx, timeout)
	require.NoError(t, err)

	sc, err = r.SendContext(ctx, x.stream, x.viewer)
	require.NoError(t, err)
	require.WithinDuration(t, short, *sc.Restriction.TimedOutUntil, time.Second)

	lifted, _, err := r.Lift(ctx, action(x, entity.ModUntimeout, x.viewer))
	require.NoError(t, err)
	require.True(t, lifted)

	sc, err = r.SendContext(ctx, x.stream, x.viewer)
	require.NoError(t, err)
	require.Nil(t, sc.Restriction.TimedOutUntil)

	lifted, _, err = r.Lift(ctx, action(x, entity.ModUnban, x.viewer))
	require.NoError(t, err)
	require.False(t, lifted, "not banned")

	_, _, err = r.Restrict(ctx, action(x, entity.ModBan, x.viewer))
	require.NoError(t, err)

	timeout.ExpiresAt = &until
	_, _, err = r.Restrict(ctx, timeout)
	require.ErrorIs(t, err, entity.ErrInvalidState, "timeout of a banned user")

	list, err := r.Restrictions(ctx, x.channel, nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, entity.RestrictBan, list[0].Type)
	require.Equal(t, "viewer_x", list[0].User.Username)
	require.Equal(t, "mod_x", list[0].Actor.Username)

	timeouts := entity.RestrictTimeout
	list, err = r.Restrictions(ctx, x.channel, &timeouts, nil, 10)
	require.NoError(t, err)
	require.Empty(t, list)

	cur, err := r.Restriction(ctx, x.channel, x.viewer)
	require.NoError(t, err)
	require.NotNil(t, cur)

	lifted, _, err = r.Lift(ctx, action(x, entity.ModUnban, x.viewer))
	require.NoError(t, err)
	require.True(t, lifted)

	cur, err = r.Restriction(ctx, x.channel, x.viewer)
	require.NoError(t, err)
	require.Nil(t, cur)

	require.Equal(t, 5, outboxCount(t, pg, events.TypeChatModAction), "timeout ×2, untimeout, ban, unban")
}

func TestDeleteAndPin(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	x := newRoom(t, r)
	first := send(t, r, x.stream, x.viewer, "first")
	second := send(t, r, x.stream, x.viewer, "second")
	stream := x.stream

	pin := func(id string) (entity.Message, error) {
		a := action(x, entity.ModPin, x.viewer)
		a.StreamID, a.MessageID = &stream, &id

		return r.Pin(ctx, a)
	}

	m, err := pin(first.ID)
	require.NoError(t, err)
	require.True(t, m.Pinned)

	_, err = pin(second.ID)
	require.NoError(t, err)

	p, err := r.Pinned(ctx, x.stream)
	require.NoError(t, err)
	require.Equal(t, second.ID, p.ID, "one pin per room")

	del := action(x, entity.ModDelete, x.viewer)
	del.StreamID, del.MessageID = &stream, &second.ID

	deleted, wasPinned, err := r.DeleteMessage(ctx, del)
	require.NoError(t, err)
	require.True(t, deleted)
	require.True(t, wasPinned)

	deleted, _, err = r.DeleteMessage(ctx, del)
	require.NoError(t, err)
	require.False(t, deleted, "already deleted")

	p, err = r.Pinned(ctx, x.stream)
	require.NoError(t, err)
	require.Nil(t, p)

	_, err = pin(second.ID)
	require.ErrorIs(t, err, entity.ErrInvalidState, "a deleted message")

	_, err = pin(uuid.NewString())
	require.ErrorIs(t, err, entity.ErrNotFound)

	unpin := action(x, entity.ModUnpin, x.viewer)
	unpin.StreamID = &stream
	unpinned, err := r.Unpin(ctx, unpin)
	require.NoError(t, err)
	require.False(t, unpinned, "nothing pinned")

	history, err := r.History(ctx, x.stream, nil, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.True(t, history[0].Deleted)

	// Evidence keeps the original text of deleted messages.
	items, err := r.Evidence(ctx, x.stream, &second.ID, nil, 10)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "first", items[0].Body)
	require.Equal(t, "second", items[1].Body)
	require.True(t, items[1].Deleted)

	byUser, err := r.Evidence(ctx, x.stream, nil, &x.viewer, 1)
	require.NoError(t, err)
	require.Len(t, byUser, 1)

	_, err = r.Evidence(ctx, uuid.NewString(), &second.ID, nil, 10)
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, err = r.CloseRoom(ctx, x.stream, 0)
	require.NoError(t, err)

	_, err = pin(first.ID)
	require.ErrorIs(t, err, entity.ErrRoomReadOnly)

	require.Equal(t, 3, outboxCount(t, pg, events.TypeChatModAction))
}

func TestTermsRolesAndMentions(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, pg := setup(t)
	ctx := context.Background()
	x := newRoom(t, r)
	audit := events.AuditRecorded{Action: "BLOCKED_TERM_ADDED", Metadata: map[string]any{}}

	term, created, err := r.AddTerm(ctx, entity.BlockedTerm{ChannelID: x.channel, Term: "spam", Display: "Spam", CreatedBy: entity.UserRef{ID: x.mod}}, 2, audit)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "mod_x", term.CreatedBy.Username)

	again, created, err := r.AddTerm(ctx, entity.BlockedTerm{ChannelID: x.channel, Term: "spam", Display: "SPAM", CreatedBy: entity.UserRef{ID: x.mod}}, 2, audit)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, term.ID, again.ID)

	_, _, err = r.AddTerm(ctx, entity.BlockedTerm{ChannelID: x.channel, Term: "abc", Display: "abc", CreatedBy: entity.UserRef{ID: x.mod}}, 2, audit)
	require.NoError(t, err)

	_, _, err = r.AddTerm(ctx, entity.BlockedTerm{ChannelID: x.channel, Term: "def", Display: "def", CreatedBy: entity.UserRef{ID: x.mod}}, 2, audit)
	require.ErrorIs(t, err, entity.ErrLimitReached)

	removed, err := r.RemoveTerm(ctx, x.channel, term.ID, events.AuditRecorded{Action: "BLOCKED_TERM_REMOVED"})
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 3, outboxCount(t, pg, events.TypeAuditRecorded))

	g := entity.RoleGrant{ChannelID: x.channel, User: entity.UserRef{ID: x.viewer}, Role: entity.ChannelRoleModerator, GrantedBy: entity.UserRef{ID: x.owner}}

	grant, created, err := r.Assign(ctx, g, 1, entity.ActorOwner)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "viewer_x", grant.User.Username)

	_, created, err = r.Assign(ctx, g, 1, entity.ActorOwner)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, 1, outboxCount(t, pg, events.TypeChannelRoleChanged), "same role: no event")

	other := g
	other.User.ID = x.mod
	_, _, err = r.Assign(ctx, other, 1, entity.ActorOwner)
	require.ErrorIs(t, err, entity.ErrLimitReached)

	g.Role = entity.ChannelRoleVIP
	grant, _, err = r.Assign(ctx, g, 1, entity.ActorOwner)
	require.NoError(t, err)
	require.Equal(t, entity.ChannelRoleModerator, *grant.Previous)

	mine, err := r.RolesOfUser(ctx, x.viewer)
	require.NoError(t, err)
	require.Len(t, mine, 1)

	info, err := r.ChannelInfo(ctx, []string{x.channel})
	require.NoError(t, err)
	require.Equal(t, "minhnn", info[x.channel].Handle)

	removed, err = r.Revoke(ctx, x.channel, x.viewer, x.owner, entity.ActorOwner)
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 3, outboxCount(t, pg, events.TypeChannelRoleChanged))

	_, err = r.InsertMessage(ctx, entity.Message{
		StreamID: x.stream, User: entity.Author{ID: x.owner, Username: "owner_x"}, ClientMsgID: uuid.NewString(), Body: "hi @viewer_x",
		Fragments: []entity.Fragment{{Type: entity.FragmentMention, Text: "@viewer_x", UserID: x.viewer}}, Type: entity.MessageNormal,
	})
	require.NoError(t, err)
	require.Equal(t, 1, outboxCount(t, pg, events.TypeChatMention))

	require.NoError(t, r.SetChannelStatus(ctx, uuid.NewString(), x.channel, entity.ChannelSuspended))

	info, err = r.ChannelInfo(ctx, []string{x.channel})
	require.NoError(t, err)
	require.Equal(t, entity.ChannelSuspended, info[x.channel].Status)

	done, err := r.BackfillDone(ctx, "channels")
	require.NoError(t, err)

	if !done {
		require.NoError(t, r.MarkBackfilled(ctx, "channels"))
	}
}
