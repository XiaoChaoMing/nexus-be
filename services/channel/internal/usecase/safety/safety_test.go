package safety_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/safety"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

type fakeStatus struct {
	got     entity.StatusChange
	changed bool
	err     error
}

func (f *fakeStatus) SetStatus(_ context.Context, _ string, c entity.StatusChange) (bool, error) {
	f.got = c

	return f.changed, f.err
}

func TestChannelActioned(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		action  string
		repo    *fakeStatus
		suspend bool
		wantErr error
	}{
		{name: "suspend", action: events.ActionSuspend, repo: &fakeStatus{changed: true}, suspend: true},
		{name: "restore", action: events.ActionRestore, repo: &fakeStatus{changed: true}},
		{name: "same state is acked", action: events.ActionSuspend, repo: &fakeStatus{}, suspend: true},
		{name: "db error is retried", action: events.ActionSuspend, repo: &fakeStatus{err: errDB}, suspend: true, wantErr: errDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uc := safety.New(tc.repo, nil, logger.New("error"))
			err := uc.ChannelActioned(context.Background(), uuid.NewString(), events.ChannelActioned{
				ActionID: uuid.NewString(), ChannelID: uuid.NewString(), Action: tc.action, Reason: "repeated spam",
			})

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.suspend, tc.repo.got.Suspend)
		})
	}
}

func TestSnapshotCursors(t *testing.T) {
	t.Parallel()

	uc := safety.New(nil, nil, logger.New("error"))

	_, _, err := uc.Channels(context.Background(), "nope", 10)
	require.ErrorIs(t, err, entity.ErrInvalidCursor)

	_, _, err = uc.Follows(context.Background(), uuid.NewString(), 10)
	require.ErrorIs(t, err, entity.ErrInvalidCursor)
}
