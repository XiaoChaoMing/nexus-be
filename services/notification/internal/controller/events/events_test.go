package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	ctrl "github.com/minhnn/nexuslive-be/services/notification/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
	"github.com/stretchr/testify/require"
)

const (
	id1 = "0199d4a2-0000-7000-8000-000000000001"
	id2 = "0199d4a2-0000-7000-8000-000000000002"
)

var errDB = errors.New("db down")

type fakeSources struct {
	usecase.Sources

	err   error
	calls []string
}

func (f *fakeSources) CreatorLive(_ context.Context, id string, _ events.StreamStarted) error {
	f.calls = append(f.calls, "live:"+id)

	return f.err
}

func (f *fakeSources) ChannelStatus(_ context.Context, _ string, suspended bool, _ events.ChannelSuspended) error {
	f.calls = append(f.calls, map[bool]string{true: "suspended", false: "restored"}[suspended])

	return f.err
}

type nopObs struct{ usecase.Observer }

func (nopObs) EventLag(string, time.Duration) {}

func event(t *testing.T, typ string, payload any) eventbus.Event {
	t.Helper()

	b, err := json.Marshal(payload)
	require.NoError(t, err)

	return eventbus.Event{ID: id1, Type: typ, Payload: b, OccurredAt: time.Now()}
}

func TestSourcesHandler(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	started := events.StreamStarted{StreamID: id1, ChannelID: id2}
	badChannel := events.StreamStarted{StreamID: id1, ChannelID: "x"}
	restored := events.ChannelSuspended{ChannelID: id1, OwnerID: id2}

	tests := []struct {
		name    string
		typ     string
		payload any
		raw     *eventbus.Event
		ucErr   error
		skip    bool
		err     error
		call    string
	}{
		{name: "stream.started", typ: events.TypeStreamStarted, payload: started, call: "live:" + id1},
		{name: "bad channel id is skipped", typ: events.TypeStreamStarted, payload: badChannel, skip: true},
		{
			name: "missing channel copy is skipped", typ: events.TypeStreamStarted, payload: started,
			ucErr: entity.ErrNotFound, skip: true, call: "live:" + id1,
		},
		{
			name: "database error is retried", typ: events.TypeStreamStarted, payload: started, ucErr: errDB, err: errDB,
			call: "live:" + id1,
		},
		{name: "channel.restored", typ: events.TypeChannelRestored, payload: restored, call: "restored"},
		{
			name: "not JSON", skip: true,
			raw: &eventbus.Event{ID: id1, Type: events.TypeChannelSuspended, Payload: []byte("{")},
		},
		{
			name: "event id not a uuid", skip: true,
			raw: &eventbus.Event{ID: "x", Type: events.TypeStreamStarted, Payload: []byte("{}")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc := &fakeSources{err: tc.ucErr}

			ev := tc.raw
			if ev == nil {
				e := event(t, tc.typ, tc.payload)
				ev = &e
			}

			err := ctrl.SourcesHandler(uc, nopObs{})(context.Background(), *ev)

			switch {
			case tc.skip:
				require.ErrorIs(t, err, eventbus.ErrSkip)
			case tc.err != nil:
				require.ErrorIs(t, err, tc.err)
				require.NotErrorIs(t, err, eventbus.ErrSkip)
			default:
				require.NoError(t, err)
			}

			if tc.call != "" {
				require.Equal(t, []string{tc.call}, uc.calls)
			}
		})
	}
}

type fakeReplica struct {
	usecase.Replica

	follows []entity.Follow
	users   []entity.UserCopy
}

func (f *fakeReplica) SetFollow(_ context.Context, _ time.Time, fl entity.Follow) error {
	f.follows = append(f.follows, fl)

	return nil
}

func (f *fakeReplica) PatchUser(_ context.Context, _ time.Time, u entity.UserCopy) error {
	f.users = append(f.users, u)

	return nil
}

func TestReplicaHandler(t *testing.T) {
	t.Parallel()

	r := &fakeReplica{}
	h := ctrl.ReplicaHandler(r)

	// follow.created published before notifyLevel existed means ALL.
	require.NoError(t, h(context.Background(), event(t, events.TypeFollowCreated, events.FollowCreated{UserID: id1, ChannelID: id2})))
	require.Equal(t, events.NotifyAll, r.follows[0].NotifyLevel)

	require.NoError(t, h(context.Background(), event(t, events.TypeUserEmailVerified, events.UserEmailVerified{UserID: id1})))
	require.True(t, *r.users[0].EmailVerified)

	err := h(context.Background(), event(t, events.TypeUserRegistered, events.UserRegistered{UserID: id1, Username: "a"}))
	require.ErrorIs(t, err, eventbus.ErrSkip, "no e-mail")
}

type fakeMail struct{ err error }

func (f fakeMail) Deliver(context.Context, events.EmailRequested) error { return f.err }

func TestAuthMailHandler(t *testing.T) {
	t.Parallel()

	ok := events.EmailRequested{UserID: id1, Email: "a@x.dev", Purpose: events.EmailPurposeVerify, Token: "t"}
	require.NoError(t, ctrl.AuthMailHandler(fakeMail{})(context.Background(), event(t, events.TypeEmailRequested, ok)))

	lock := events.EmailRequested{UserID: id1, Email: "a@x.dev", Purpose: events.EmailPurposeSecurityLock}
	require.NoError(t, ctrl.AuthMailHandler(fakeMail{})(context.Background(), event(t, events.TypeEmailRequested, lock)))

	noToken := ok
	noToken.Token = ""
	require.ErrorIs(t, ctrl.AuthMailHandler(fakeMail{})(context.Background(), event(t, events.TypeEmailRequested, noToken)),
		eventbus.ErrSkip)

	require.ErrorIs(t, ctrl.AuthMailHandler(fakeMail{err: entity.ErrUnknownPurpose})(context.Background(),
		event(t, events.TypeEmailRequested, ok)), eventbus.ErrSkip)

	err := ctrl.AuthMailHandler(fakeMail{err: errDB})(context.Background(), event(t, events.TypeEmailRequested, ok))
	require.ErrorIs(t, err, errDB)
	require.NotErrorIs(t, err, eventbus.ErrSkip)
}
