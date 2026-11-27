package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	handler "github.com/minhnn/nexuslive-be/services/safety/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
	"github.com/stretchr/testify/require"
)

type sink struct {
	users    []repo.UserPatch
	channels []repo.ChannelPatch
	streams  []repo.StreamPatch
	vods     []repo.VODPatch
	audit    []entity.AuditEntry
	promote  []*string
}

func (s *sink) PatchUser(_ context.Context, _ string, _ time.Time, p repo.UserPatch) error {
	s.users = append(s.users, p)

	return nil
}

func (s *sink) PatchChannel(_ context.Context, _ string, _ time.Time, p repo.ChannelPatch) error {
	s.channels = append(s.channels, p)

	return nil
}

func (s *sink) PatchStream(_ context.Context, _ string, _ time.Time, p repo.StreamPatch) error {
	s.streams = append(s.streams, p)

	return nil
}

func (s *sink) PatchVOD(_ context.Context, _ string, _ time.Time, p repo.VODPatch) error {
	s.vods = append(s.vods, p)

	return nil
}

func (s *sink) Backfill(context.Context) error { return nil }

type auditSink struct{ *sink }

func (a auditSink) Record(_ context.Context, e entity.AuditEntry, promote *string) error {
	a.audit = append(a.audit, e)
	a.promote = append(a.promote, promote)

	return nil
}

func (auditSink) List(context.Context, usecase.AuditQuery) (usecase.Page[entity.AuditEntry], error) {
	return usecase.Page[entity.AuditEntry]{}, nil
}

func event(t *testing.T, typ string, payload any) eventbus.Event {
	t.Helper()

	b, err := json.Marshal(payload)
	require.NoError(t, err)

	return eventbus.Event{ID: uuid.NewString(), Type: typ, Producer: "chat-svc", OccurredAt: time.Now(), Payload: b}
}

func TestHandler(t *testing.T) {
	t.Parallel()

	s := &sink{}
	h := handler.Handler(s, auditSink{s})
	ctx := context.Background()
	user, channel, stream, msg := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	until := time.Now().Add(time.Hour)

	for _, e := range []eventbus.Event{
		event(t, events.TypeUserRegistered, events.UserRegistered{UserID: user, Username: "an", DisplayName: "An"}),
		event(t, events.TypeUserLocked, events.UserLocked{UserID: user, LockedUntil: &until}),
		event(t, events.TypeChannelCreated, events.ChannelCreated{ChannelID: channel, OwnerID: user, Handle: "an", DisplayName: "An"}),
		event(t, events.TypeChannelSuspended, events.ChannelSuspended{ChannelID: channel, OwnerID: user}),
		event(t, events.TypeStreamStarted, events.StreamStarted{StreamID: stream, ChannelID: channel, Title: "t"}),
		event(t, events.TypeStreamEnded, events.StreamEnded{StreamID: stream, ChannelID: channel, EndReason: events.EndReasonAdminTakedown}),
		event(t, events.TypeChatModAction, events.ChatModAction{
			ChannelID: channel, ActorID: user, ActorRole: events.ActorModerator,
			Type: events.ModDelete, MessageID: &msg, TargetUserID: &user, MessageBody: ptr("bad"),
		}),
		event(t, events.TypeChannelRoleChanged, events.ChannelRoleChanged{
			ChannelID: channel, UserID: user, ActorID: user,
			ActorRole: events.ActorOwner, Role: ptr("MODERATOR"),
		}),
		event(t, events.TypeAuditRecorded, events.AuditRecorded{
			ActorRole: events.ActorAdmin, Action: "ADMIN_PROMOTED",
			ResourceType: "USER", ResourceID: &user,
		}),
	} {
		require.NoError(t, h(ctx, e), e.Type)
	}

	require.Len(t, s.users, 2)
	require.Equal(t, entity.UserLocked, *s.users[1].Status)
	require.Len(t, s.channels, 2)
	require.Equal(t, entity.ChannelSuspended, *s.channels[1].Status)
	require.Len(t, s.streams, 2)
	require.Equal(t, events.EndReasonAdminTakedown, *s.streams[1].EndReason)

	require.Len(t, s.audit, 3)
	require.Equal(t, "CHAT_DELETE", s.audit[0].Action)
	require.Equal(t, entity.TargetMessage, s.audit[0].ResourceType)
	require.Equal(t, "bad", s.audit[0].Metadata["messageBody"])
	require.Equal(t, "chat-svc", s.audit[0].SourceService)
	require.NotNil(t, s.audit[0].SourceEventID)
	require.Equal(t, "CHANNEL_ROLE_GRANTED", s.audit[1].Action)
	require.Equal(t, &user, s.promote[2])
}

func TestHandlerSkips(t *testing.T) {
	t.Parallel()

	s := &sink{}
	h := handler.Handler(s, auditSink{s})
	ctx := context.Background()

	bad := []eventbus.Event{
		{ID: "x", Type: events.TypeUserLocked},
		event(t, events.TypeUserLocked, events.UserLocked{UserID: "nope"}),
		event(t, events.TypeStreamStatusChanged, events.StreamStatusChanged{StreamID: uuid.NewString(), ChannelID: uuid.NewString(), Status: "WEIRD"}),
		event(t, events.TypeChatModAction, events.ChatModAction{ChannelID: uuid.NewString(), ActorID: uuid.NewString(), Type: "NUKE"}),
		{ID: uuid.NewString(), Type: events.TypeAuditRecorded, Payload: []byte(`{`)},
		event(t, events.TypeVODDeleted, events.VODDeleted{VODID: uuid.NewString(), ChannelID: uuid.NewString(), StreamID: "x"}),
		event(t, events.TypeVODDeleted, events.VODDeleted{VODID: "x", ChannelID: uuid.NewString()}),
		event(t, events.TypeVODPublished, events.VODPublished{VODID: uuid.NewString(), ChannelID: uuid.NewString(), Source: "FILE"}),
		event(t, events.TypeVODUpdated, events.VODUpdated{VODID: uuid.NewString()}),
	}

	for _, e := range bad {
		require.ErrorIs(t, h(ctx, e), eventbus.ErrSkip, e.Type)
	}
}

func TestHandlerVODs(t *testing.T) {
	t.Parallel()

	s := &sink{}
	h := handler.Handler(s, auditSink{s})
	ctx := context.Background()
	channel, stream, rec, upload := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()

	for _, e := range []eventbus.Event{
		event(t, events.TypeStreamVODReady, events.StreamVODReady{StreamID: stream, ChannelID: channel, VODID: rec, Title: "Rec"}),
		event(t, events.TypeVODPublished, events.VODPublished{VODID: upload, ChannelID: channel, Source: events.VODSourceUpload, Title: "Up"}),
		event(t, events.TypeVODUpdated, events.VODUpdated{VODID: upload, ChannelID: channel, Title: "Up 2", Source: events.VODSourceUpload}),
		event(t, events.TypeVODDeleted, events.VODDeleted{VODID: upload, ChannelID: channel, Source: events.VODSourceUpload}),
		// A recording deleted before source existed.
		event(t, events.TypeVODDeleted, events.VODDeleted{VODID: rec, ChannelID: channel, StreamID: stream}),
	} {
		require.NoError(t, h(ctx, e), e.Type)
	}

	require.Empty(t, s.streams, "VOD events patch the stream copy through the VOD patch")
	require.Len(t, s.vods, 5)

	ready := s.vods[0]
	require.Equal(t, entity.VODSourceLive, *ready.Source)
	require.Equal(t, stream, *ready.StreamID)
	require.Equal(t, repo.StreamPatch{ID: stream, ChannelID: channel, Title: ptr("Rec"), VODID: &rec}, *ready.Stream)

	published := s.vods[1]
	require.Equal(t, entity.VODSourceUpload, *published.Source)
	require.Nil(t, published.StreamID)
	require.Nil(t, published.Stream)

	require.Equal(t, "Up 2", *s.vods[2].Title)
	require.Nil(t, s.vods[2].StreamID)

	deletedUpload := s.vods[3]
	require.True(t, deletedUpload.Deleted)
	require.Nil(t, deletedUpload.Stream, "an uploaded video has no stream copy to patch")

	deletedRec := s.vods[4]
	require.True(t, deletedRec.Deleted)
	require.Equal(t, entity.VODSourceLive, *deletedRec.Source)
	require.Equal(t, repo.StreamPatch{ID: stream, ChannelID: channel, VODID: &rec, VODDeleted: true}, *deletedRec.Stream)
}

func ptr[T any](v T) *T { return &v }
