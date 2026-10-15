package notify_test

import (
	"context"
	"testing"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestCenterRejectsBadInput(t *testing.T) {
	t.Parallel()

	e := newEnv()

	_, err := e.uc.List(context.Background(), viewerID, false, "not-a-cursor", 20)
	require.ErrorIs(t, err, entity.ErrBadCursor)

	require.ErrorIs(t, e.uc.Read(context.Background(), viewerID, "123"), entity.ErrNotFound)
	require.ErrorIs(t, e.uc.Delete(context.Background(), viewerID, "x"), entity.ErrNotFound)
}
