package vods_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/vods"
	"github.com/stretchr/testify/require"
)

var errS3 = errors.New("s3 down")

type remover struct {
	prefixes []string
	err      error
}

func (r *remover) RemovePrefix(_ context.Context, prefix string) (int, error) {
	r.prefixes = append(r.prefixes, prefix)

	return 3, r.err
}

type counter struct{ n int }

func (c *counter) VODObjectsDeleted(n int) { c.n += n }

func TestDeletedHandler(t *testing.T) {
	t.Parallel()

	stream := uuid.NewString()

	tests := []struct {
		name    string
		payload string
		err     error
		skip    bool
		wantErr bool
	}{
		{name: "removed", payload: `{"vodId":"v","streamId":"` + stream + `"}`},
		{name: "storage down: redelivered", payload: `{"vodId":"v","streamId":"` + stream + `"}`, err: errS3, wantErr: true},
		{name: "path traversal", payload: `{"vodId":"v","streamId":"../"}`, skip: true},
		{name: "not json", payload: `{`, skip: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, c := &remover{err: tc.err}, &counter{}
			err := vods.DeletedHandler(r, c, logger.New("error"))(context.Background(), eventbus.Event{Payload: []byte(tc.payload)})

			switch {
			case tc.skip:
				require.ErrorIs(t, err, eventbus.ErrSkip)
				require.Empty(t, r.prefixes)
			case tc.wantErr:
				require.ErrorIs(t, err, errS3)
				require.NotErrorIs(t, err, eventbus.ErrSkip)
			default:
				require.NoError(t, err)
				require.Equal(t, []string{"streams/" + stream + "/"}, r.prefixes)
				require.Equal(t, 3, c.n)
			}
		})
	}
}
