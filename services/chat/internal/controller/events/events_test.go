package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	pkgevents "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/stretchr/testify/require"
)

type rec struct {
	usecase.Rooms
	usecase.Grants
	calls []string
}

func (r *rec) StreamStarted(context.Context, pkgevents.StreamStarted) error {
	r.calls = append(r.calls, "started")

	return nil
}

func (r *rec) StreamEnded(context.Context, pkgevents.StreamEnded) error {
	r.calls = append(r.calls, "ended")

	return nil
}

func (r *rec) StreamStatusChanged(_ context.Context, e pkgevents.StreamStatusChanged) error {
	r.calls = append(r.calls, "status "+e.Status)

	return nil
}

func (r *rec) UserRegistered(_ context.Context, _ string, u entity.User) error {
	r.calls = append(r.calls, "user "+u.Username)

	return nil
}

func (r *rec) ChannelCreated(_ context.Context, _ string, c entity.Channel) error {
	r.calls = append(r.calls, "channel "+c.OwnerID)

	return nil
}

func (r *rec) Follow(_ context.Context, _, _ string, at time.Time) error {
	r.calls = append(r.calls, "follow "+at.UTC().Format(time.RFC3339))

	return nil
}

func TestHandler(t *testing.T) {
	t.Parallel()

	stream, ch, user := uuid.NewString(), uuid.NewString(), uuid.NewString()

	tests := []struct {
		name     string
		typ      string
		payload  string
		wantCall string
		wantSkip bool
	}{
		{name: "started", typ: pkgevents.TypeStreamStarted, payload: `{"streamId":"` + stream + `","channelId":"` + ch + `"}`, wantCall: "started"},
		{name: "started without channel", typ: pkgevents.TypeStreamStarted, payload: `{"streamId":"` + stream + `"}`, wantSkip: true},
		{name: "ended", typ: pkgevents.TypeStreamEnded, payload: `{"streamId":"` + stream + `","endReason":"creator"}`, wantCall: "ended"},
		{name: "interrupted", typ: pkgevents.TypeStreamStatusChanged, payload: `{"streamId":"` + stream + `","status":"INTERRUPTED"}`, wantCall: "status INTERRUPTED"},
		{name: "unknown status", typ: pkgevents.TypeStreamStatusChanged, payload: `{"streamId":"` + stream + `","status":"ENDED"}`, wantSkip: true},
		{name: "user", typ: pkgevents.TypeUserRegistered, payload: `{"userId":"` + user + `","username":"minhnn"}`, wantCall: "user minhnn"},
		{name: "user without name", typ: pkgevents.TypeUserRegistered, payload: `{"userId":"` + user + `"}`, wantSkip: true},
		{name: "channel", typ: pkgevents.TypeChannelCreated, payload: `{"channelId":"` + ch + `","ownerId":"` + user + `"}`, wantCall: "channel " + user},
		{
			name: "follow", typ: pkgevents.TypeFollowCreated,
			payload:  `{"userId":"` + user + `","channelId":"` + ch + `","createdAt":"2026-10-14T10:00:00Z"}`,
			wantCall: "follow 2026-10-14T10:00:00Z",
		},
		{name: "not json", typ: pkgevents.TypeStreamEnded, payload: `{`, wantSkip: true},
		{name: "ignored type", typ: "ingest.health.v1", payload: `{}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &rec{}
			err := events.Handler(r, r)(context.Background(), eventbus.Event{ID: uuid.NewString(), Type: tc.typ, Payload: []byte(tc.payload)})

			if tc.wantSkip {
				require.ErrorIs(t, err, eventbus.ErrSkip)
				require.Empty(t, r.calls)

				return
			}

			require.NoError(t, err)

			if tc.wantCall != "" {
				require.Equal(t, []string{tc.wantCall}, r.calls)
			}
		})
	}

	require.Contains(t, events.Types, pkgevents.TypeFollowDeleted)
}

func TestHandlerRejectsBadEventID(t *testing.T) {
	t.Parallel()

	r := &rec{}
	err := events.Handler(r, r)(context.Background(), eventbus.Event{
		ID: "x", Type: pkgevents.TypeStreamEnded, Payload: []byte(`{"streamId":"` + uuid.NewString() + `"}`),
	})
	require.ErrorIs(t, err, eventbus.ErrSkip)
}
