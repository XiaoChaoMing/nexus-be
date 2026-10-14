package v1_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	v1 "github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/stretchr/testify/require"
)

// fakeStudio: user "creator" owns channel "ch1"; everyone else owns none.
type fakeStudio struct {
	usecase.Studio
	keysFor []string
}

func (*fakeStudio) Channel(_ context.Context, ownerID string) (entity.Channel, error) {
	if ownerID != "creator" {
		return entity.Channel{}, entity.ErrNotCreator
	}

	return entity.Channel{ID: "ch1", OwnerID: ownerID, Handle: "minhnn", DisplayName: "Minh"}, nil
}

func (f *fakeStudio) KeyInfo(_ context.Context, channelID string) (entity.StreamKeyInfo, error) {
	f.keysFor = append(f.keysFor, channelID)

	return entity.StreamKeyInfo{}, nil
}

func (*fakeStudio) Overview(_ context.Context, channelID string) (usecase.StudioOverview, error) {
	return usecase.StudioOverview{Settings: entity.Settings{ChannelID: channelID}}, nil
}

type fakeCatalog struct{ usecase.Catalog }

func (fakeCatalog) GetFor(_ context.Context, id, _ string) (entity.Stream, error) {
	s := entity.Stream{ID: id, ChannelID: "ch1", Status: entity.StatusLive}
	if id == "0199d4a2-0000-7000-8000-000000000001" {
		s.Channel = &entity.Channel{ID: "ch1", Handle: "minhnn", DisplayName: "Minh"}
	}

	return s, nil
}

func newApp(studio usecase.Studio) *fiber.App {
	return newAppWith(studio, &fakeViewer{})
}

func newAppWith(studio usecase.Studio, viewer usecase.Viewer) *fiber.App {
	app := fiber.New()
	// Stands in for middleware.Auth: the "X-User" header becomes the authenticated user id.
	auth := func(ctx *fiber.Ctx) error {
		if u := ctx.Get("X-User"); u != "" {
			ctx.Locals("userID", u)
		}

		return ctx.Next()
	}

	v1.NewRoutes(app.Group("/v1"), studio, fakeCatalog{}, viewer, v1.Auth{Required: auth, Optional: auth},
		logger.New("error"), v1.Options{HLSBase: "/hls"})

	return app
}

func do(t *testing.T, app *fiber.App, method, path, user string) (status int, body []byte) {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	if user != "" {
		req.Header.Set("X-User", user)
	}

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, body
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()

	var v T
	require.NoError(t, json.Unmarshal(b, &v))

	return v
}

func TestStudioRequiresChannel(t *testing.T) {
	t.Parallel()

	routes := []struct{ method, path string }{
		{http.MethodPost, "/v1/studio/stream-key"},
		{http.MethodGet, "/v1/studio/stream-key"},
		{http.MethodGet, "/v1/studio/stream"},
		{http.MethodPut, "/v1/studio/stream"},
		{http.MethodPost, "/v1/studio/stream/end"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			t.Parallel()

			app := newApp(&fakeStudio{})

			status, body := do(t, app, rt.method, rt.path, "viewer")
			require.Equal(t, http.StatusForbidden, status)
			require.Equal(t, v1.CodeNotCreator, decode[v1.ErrorResponse](t, body).Error.Code)

			status, _ = do(t, app, rt.method, rt.path, "")
			require.Equal(t, http.StatusUnauthorized, status)
		})
	}
}

func TestStudioUsesOwnersChannel(t *testing.T) {
	t.Parallel()

	studio := &fakeStudio{}
	app := newApp(studio)

	status, _ := do(t, app, http.MethodGet, "/v1/studio/stream-key", "creator")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{"ch1"}, studio.keysFor, "channel id comes from creator_channels, not the user id")

	status, body := do(t, app, http.MethodGet, "/v1/studio/stream", "creator")
	require.Equal(t, http.StatusOK, status)

	require.Equal(t, v1.Channel{ID: "ch1", Handle: "minhnn", DisplayName: "Minh"}, decode[v1.Data[v1.Studio]](t, body).Data.Channel)
	require.Contains(t, string(body), `"avatarUrl":null`)
}

func TestStreamChannelField(t *testing.T) {
	t.Parallel()

	app := newApp(&fakeStudio{})

	status, body := do(t, app, http.MethodGet, "/v1/streams/0199d4a2-0000-7000-8000-000000000001", "")
	require.Equal(t, http.StatusOK, status)

	data := decode[v1.Data[v1.Stream]](t, body).Data
	require.Equal(t, "ch1", data.ChannelID)
	require.NotNil(t, data.Channel)
	require.Equal(t, "minhnn", data.Channel.Handle)

	status, body = do(t, app, http.MethodGet, "/v1/streams/0199d4a2-0000-7000-8000-000000000002", "")
	require.Equal(t, http.StatusOK, status)

	require.Nil(t, decode[v1.Data[v1.Stream]](t, body).Data.Channel)
	require.Contains(t, string(body), `"channel":null`, "unknown channel is null, not missing")
}
