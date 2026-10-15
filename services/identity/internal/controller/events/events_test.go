package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	pkgevents "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errTemporary = errors.New("temporary")

type fakeSync struct {
	err  error
	got  []entity.UserChannel
	ids  []string
	call int
}

func (f *fakeSync) ChannelCreated(_ context.Context, eventID string, ch entity.UserChannel) error {
	f.call++
	f.ids = append(f.ids, eventID)
	f.got = append(f.got, ch)

	return f.err
}

type fakeMail struct {
	err error
	got []usecase.MailRequest
}

func (f *fakeMail) Deliver(_ context.Context, m usecase.MailRequest) error {
	f.got = append(f.got, m)

	return f.err
}

func payload(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)

	return b
}

func TestChannelHandler(t *testing.T) {
	t.Parallel()

	good := pkgevents.ChannelCreated{ChannelID: uuid.NewString(), OwnerID: uuid.NewString(), Handle: "minhnn", DisplayName: "Minh"}

	tests := []struct {
		name     string
		payload  []byte
		id       string
		syncErr  error
		wantSkip bool
		wantErr  error
		wantCall int
	}{
		{name: "stored", payload: payload(t, good), id: uuid.NewString(), wantCall: 1},
		{name: "not json", payload: []byte("{"), id: uuid.NewString(), wantSkip: true},
		{name: "bad owner id", payload: payload(t, pkgevents.ChannelCreated{ChannelID: uuid.NewString(), OwnerID: "x", Handle: "h", DisplayName: "d"}), wantSkip: true},
		{name: "bad event id", payload: payload(t, good), id: "nope", wantSkip: true},
		{name: "unknown owner", payload: payload(t, good), syncErr: entity.ErrUserNotFound, wantSkip: true, wantCall: 1},
		{name: "channel of another user", payload: payload(t, good), syncErr: entity.ErrChannelTaken, wantSkip: true, wantCall: 1},
		{name: "database down is retried", payload: payload(t, good), syncErr: errTemporary, wantErr: errTemporary, wantCall: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &fakeSync{err: tc.syncErr}
			err := events.ChannelHandler(s)(context.Background(), eventbus.Event{
				ID: tc.id, Type: pkgevents.TypeChannelCreated, Payload: tc.payload,
			})

			assert.Equal(t, tc.wantCall, s.call)

			switch {
			case tc.wantSkip:
				require.ErrorIs(t, err, eventbus.ErrSkip)
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
				require.NotErrorIs(t, err, eventbus.ErrSkip)
			default:
				require.NoError(t, err)
				assert.Equal(t, good.OwnerID, s.got[0].UserID)
				assert.Equal(t, tc.id, s.ids[0])
			}
		})
	}
}

func TestMailHandler(t *testing.T) {
	t.Parallel()

	good := pkgevents.EmailRequested{UserID: uuid.NewString(), Email: "a@b.c", Username: "a", Purpose: "VERIFY", Token: "t", Locale: "en"}
	bad := good
	bad.Purpose = "WELCOME"

	tests := []struct {
		name     string
		payload  []byte
		mailErr  error
		wantSkip bool
		wantErr  error
		wantSent int
	}{
		{name: "delivered", payload: payload(t, good), wantSent: 1},
		{name: "unknown purpose", payload: payload(t, bad), wantSkip: true},
		{name: "smtp failure is retried", payload: payload(t, good), mailErr: errTemporary, wantErr: errTemporary, wantSent: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := &fakeMail{err: tc.mailErr}
			err := events.MailHandler(m)(context.Background(), eventbus.Event{
				ID: uuid.NewString(), Type: pkgevents.TypeEmailRequested, Payload: tc.payload,
			})

			assert.Len(t, m.got, tc.wantSent)

			switch {
			case tc.wantSkip:
				require.ErrorIs(t, err, eventbus.ErrSkip)
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
				require.NotErrorIs(t, err, eventbus.ErrSkip)
			default:
				require.NoError(t, err)
				assert.Equal(t, entity.TokenVerify, m.got[0].Purpose)
				assert.Equal(t, "en", m.got[0].Locale)
			}
		})
	}
}

type fakeModeration struct {
	got []pkgevents.UserActioned
}

func (f *fakeModeration) Apply(_ context.Context, _ string, e pkgevents.UserActioned) error {
	f.got = append(f.got, e)

	return nil
}

func TestSafetyHandler(t *testing.T) {
	t.Parallel()

	good := pkgevents.UserActioned{ActionID: uuid.NewString(), UserID: uuid.NewString(), Action: pkgevents.ActionLock, Reason: "spam"}

	tests := []struct {
		name    string
		id      string
		payload any
		skip    bool
	}{
		{name: "lock", id: uuid.NewString(), payload: good},
		{name: "unlock", id: uuid.NewString(), payload: pkgevents.UserActioned{ActionID: good.ActionID, UserID: good.UserID, Action: pkgevents.ActionUnlock}},
		{name: "unknown action", id: uuid.NewString(), payload: pkgevents.UserActioned{ActionID: good.ActionID, UserID: good.UserID, Action: "BAN"}, skip: true},
		{name: "bad user id", id: uuid.NewString(), payload: pkgevents.UserActioned{ActionID: good.ActionID, UserID: "x", Action: pkgevents.ActionLock}, skip: true},
		{name: "no event id", id: "", payload: good, skip: true},
		{name: "not json", id: uuid.NewString(), payload: "{", skip: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mod := &fakeModeration{}
			h := events.Handler(&fakeSync{}, mod)

			err := h(context.Background(), eventbus.Event{ID: tc.id, Type: pkgevents.TypeUserActioned, Payload: payload(t, tc.payload)})
			if tc.skip {
				require.ErrorIs(t, err, eventbus.ErrSkip)
				assert.Empty(t, mod.got)

				return
			}

			require.NoError(t, err)
			assert.Len(t, mod.got, 1)
		})
	}
}
