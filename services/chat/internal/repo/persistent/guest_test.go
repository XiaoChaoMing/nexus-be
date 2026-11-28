package persistent_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestGuestChat(t *testing.T) { //nolint:paralleltest,funlen // shared database, one story
	r, _ := setup(t)
	ctx := context.Background()
	x := newRoom(t, r)
	key := entity.GuestKey([]byte("s"), uuid.NewString())

	// The flag is stored, read back and part of the send context.
	_, err := r.SaveSettings(ctx, entity.ChatSettings{ChannelID: x.channel, GuestChat: true, UpdatedBy: &x.owner}, nil)
	require.NoError(t, err)

	s, err := r.Settings(ctx, x.channel)
	require.NoError(t, err)
	require.True(t, s.GuestChat)

	sc, err := r.SendContext(ctx, x.stream, key)
	require.NoError(t, err)
	require.True(t, sc.Settings.GuestChat)
	require.Nil(t, sc.User, "a guest key has no profile")
	require.Nil(t, sc.Role)

	_, err = r.GuestName(ctx, x.channel, key)
	require.ErrorIs(t, err, entity.ErrNotFound, "never chatted")

	// A guest's message keeps its nickname; the next one may use another.
	first, err := r.InsertMessage(ctx, entity.Message{
		StreamID: x.stream, User: entity.GuestAuthor(key, "Mèo Con"), ClientMsgID: uuid.NewString(), Body: "hi",
		Fragments: []entity.Fragment{}, Type: entity.MessageNormal,
	})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	_, err = r.InsertMessage(ctx, entity.Message{
		StreamID: x.stream, User: entity.GuestAuthor(key, "Mèo Lớn"), ClientMsgID: uuid.NewString(), Body: "again",
		Fragments: []entity.Fragment{}, Type: entity.MessageNormal,
	})
	require.NoError(t, err)

	account := send(t, r, x.stream, x.viewer, "user")

	page, err := r.History(ctx, x.stream, nil, 10)
	require.NoError(t, err)
	require.Len(t, page, 3)
	require.Equal(t, entity.Author{ID: x.viewer, Username: "viewer_x", DisplayName: "viewer_x"}, page[0].User, "accounts unchanged")
	require.Equal(t, entity.GuestAuthor(key, "Mèo Lớn"), page[1].User)
	require.Equal(t, entity.GuestAuthor(key, "Mèo Con"), page[2].User)

	got, err := r.Message(ctx, x.stream, first.ID)
	require.NoError(t, err)
	require.True(t, got.User.IsGuest())

	pin := action(x, entity.ModPin, "")
	pin.TargetUserID, pin.StreamID, pin.MessageID = nil, &x.stream, &first.ID
	pinned, err := r.Pin(ctx, pin)
	require.NoError(t, err)
	require.Equal(t, "Mèo Con", pinned.User.DisplayName)

	cur, err := r.Pinned(ctx, x.stream)
	require.NoError(t, err)
	require.True(t, cur.User.IsGuest())

	name, err := r.GuestName(ctx, x.channel, key)
	require.NoError(t, err)
	require.Equal(t, "Mèo Lớn", name, "the latest nickname")

	_, err = r.GuestName(ctx, uuid.NewString(), key)
	require.ErrorIs(t, err, entity.ErrNotFound, "only the channel's rooms count")

	// Moderation by guest key: messages cleared, restriction in force, listed with the nickname.
	timeout := action(x, entity.ModTimeout, key)
	until := time.Now().Add(time.Hour)
	timeout.ExpiresAt, timeout.TargetGuestName = &until, &name

	_, unpinned, err := r.Restrict(ctx, timeout)
	require.NoError(t, err)
	require.Equal(t, []string{x.stream}, unpinned)

	got, err = r.Message(ctx, x.stream, first.ID)
	require.NoError(t, err)
	require.True(t, got.Deleted)

	sc, err = r.SendContext(ctx, x.stream, key)
	require.NoError(t, err)
	require.NotNil(t, sc.Restriction.TimedOutUntil)

	accountTimeout := action(x, entity.ModTimeout, x.viewer)
	accountTimeout.ExpiresAt = &until
	_, _, err = r.Restrict(ctx, accountTimeout)
	require.NoError(t, err)

	list, err := r.Restrictions(ctx, x.channel, nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.False(t, list[0].Guest)
	require.Equal(t, "viewer_x", list[0].User.Username)
	require.True(t, list[1].Guest)
	require.Equal(t, entity.UserRef{ID: key, DisplayName: "Mèo Lớn"}, list[1].User)

	accountMsg, err := r.Message(ctx, x.stream, account.ID)
	require.NoError(t, err)
	require.False(t, accountMsg.User.IsGuest())

	lifted, _, err := r.Lift(ctx, action(x, entity.ModUntimeout, key))
	require.NoError(t, err)
	require.True(t, lifted)

	cur2, err := r.Restriction(ctx, x.channel, key)
	require.NoError(t, err)
	require.Nil(t, cur2)

	// New daily partitions keep the column (LIKE chat_messages).
	_, err = r.EnsurePartitions(ctx, time.Now().Add(72*time.Hour), 1)
	require.NoError(t, err)
}
