package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	v1 "github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

type fakeDirectory struct{}

func (fakeDirectory) ByHandle(_ context.Context, handle string) (entity.Channel, error) {
	switch handle {
	case "minhnn":
		return entity.Channel{ID: "c1", Handle: "minhnn", DisplayName: "Minh", IsLive: true, CreatedAt: time.Unix(0, 0).UTC()}, nil
	case "banned":
		return entity.Channel{}, entity.ErrSuspended
	case "broken":
		return entity.Channel{}, errDB
	}

	return entity.Channel{}, entity.ErrNotFound
}

func TestByHandle(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	v1.NewRoutes(app.Group("/v1"), fakeDirectory{}, logger.New("error"))

	tests := []struct {
		handle   string
		status   int
		wantCode string
	}{
		{handle: "minhnn", status: http.StatusOK},
		{handle: "nobody", status: http.StatusNotFound, wantCode: v1.CodeNotFound},
		{handle: "banned", status: http.StatusForbidden, wantCode: v1.CodeChannelSuspended},
		{handle: "broken", status: http.StatusInternalServerError, wantCode: v1.CodeInternal},
	}

	for _, tc := range tests {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/channels/"+tc.handle, http.NoBody)

		resp, err := app.Test(req)
		require.NoError(t, err)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, tc.status, resp.StatusCode, tc.handle)

		if tc.wantCode != "" {
			var e v1.ErrorResponse
			require.NoError(t, json.Unmarshal(body, &e))
			require.Equal(t, tc.wantCode, e.Error.Code)
			require.NotContains(t, string(body), "db down", "no internal details leak")

			continue
		}

		var ok v1.Data[v1.Channel]
		require.NoError(t, json.Unmarshal(body, &ok))
		require.Equal(t, "c1", ok.Data.ID)
		require.True(t, ok.Data.IsLive)
		require.JSONEq(t, `{"data":{"id":"c1","handle":"minhnn","displayName":"Minh","bio":null,"avatarUrl":null,`+
			`"isLive":true,"currentStreamId":null,"createdAt":"1970-01-01T00:00:00Z"}}`, string(body))
	}
}
