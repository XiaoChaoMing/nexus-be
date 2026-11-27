package safety_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/safety"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

type fakeRepo struct {
	res       entity.TakedownResult
	err       error
	suspended []bool
}

func (f *fakeRepo) Takedown(context.Context, string, string) (entity.TakedownResult, error) {
	return f.res, f.err
}

func (f *fakeRepo) SetSuspended(_ context.Context, _, _ string, s bool) (ended, cancelled int, err error) {
	f.suspended = append(f.suspended, s)

	return 1, 2, f.err
}

func (f *fakeRepo) Streams(context.Context, string, int) ([]entity.StreamCopy, error) {
	return nil, f.err
}

func (f *fakeRepo) TakedownVOD(context.Context, string, string) (bool, error) {
	return f.res.Found, f.err
}

func (f *fakeRepo) VODCopy(_ context.Context, id string) (entity.VOD, error) {
	if f.err != nil {
		return entity.VOD{}, f.err
	}

	if !f.res.Found {
		return entity.VOD{}, entity.ErrNotFound
	}

	return entity.VOD{ID: id, Status: entity.VODDeleted}, nil
}

func TestTakedown(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		repo *fakeRepo
		want error
	}{
		{name: "live stream", repo: &fakeRepo{res: entity.TakedownResult{Found: true, Ended: true}}},
		{name: "ended stream with a VOD", repo: &fakeRepo{res: entity.TakedownResult{Found: true, VODsDeleted: 1}}},
		{name: "scheduled or unknown is acked", repo: &fakeRepo{}},
		{name: "db error is retried", repo: &fakeRepo{err: errDB}, want: errDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := safety.New(tc.repo, logger.New("error")).Takedown(context.Background(), uuid.NewString(),
				events.StreamTakedown{StreamID: uuid.NewString(), ActionID: uuid.NewString()})
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestSuspension(t *testing.T) {
	t.Parallel()

	r := &fakeRepo{}
	uc := safety.New(r, logger.New("error"))
	ctx := context.Background()

	require.NoError(t, uc.ChannelSuspended(ctx, uuid.NewString(), events.ChannelSuspended{ChannelID: uuid.NewString()}))
	require.NoError(t, uc.ChannelRestored(ctx, uuid.NewString(), events.ChannelSuspended{ChannelID: uuid.NewString()}))
	require.Equal(t, []bool{true, false}, r.suspended)

	_, _, err := uc.Streams(ctx, "x", 10)
	require.ErrorIs(t, err, entity.ErrInvalidCursor)
}

func TestVODTakedown(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		repo *fakeRepo
		want error
	}{
		{name: "vod deleted", repo: &fakeRepo{res: entity.TakedownResult{Found: true}}},
		{name: "unknown or already deleted is acked", repo: &fakeRepo{}},
		{name: "db error is retried", repo: &fakeRepo{err: errDB}, want: errDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := safety.New(tc.repo, logger.New("error")).VODTakedown(context.Background(), uuid.NewString(),
				events.VODTakedown{VODID: uuid.NewString(), ActionID: uuid.NewString()})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestVODCopy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		repo *fakeRepo
		id   string
		want error
	}{
		{name: "deleted vod is returned", repo: &fakeRepo{res: entity.TakedownResult{Found: true}}, id: uuid.NewString()},
		{name: "unknown", repo: &fakeRepo{}, id: uuid.NewString(), want: entity.ErrNotFound},
		{name: "not a uuid", repo: &fakeRepo{}, id: "x", want: entity.ErrNotFound},
		{name: "db error", repo: &fakeRepo{err: errDB}, id: uuid.NewString(), want: errDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v, err := safety.New(tc.repo, logger.New("error")).VOD(context.Background(), tc.id)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.id, v.ID)
		})
	}
}
