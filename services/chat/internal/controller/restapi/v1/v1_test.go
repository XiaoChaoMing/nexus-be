package v1_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	v1 "github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/stretchr/testify/require"
)

const (
	streamID  = "0199d4a2-0000-7000-8000-0000000000aa"
	channelID = "0199d4a2-0000-7000-8000-0000000000cc"
)

type fakeChat struct {
	usecase.Chat
	gotBefore *string
	gotLimit  int
	updatedBy string
}

func (f *fakeChat) History(_ context.Context, id string, before *string, limit int) ([]entity.Message, *string, error) {
	if id != streamID {
		return nil, nil, entity.ErrNotFound
	}

	f.gotBefore, f.gotLimit = before, limit
	next := "m1"

	return []entity.Message{{
		ID: "m1", StreamID: id, Body: "gg", Type: "NORMAL", CreatedAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
		User:      entity.Author{ID: "u1", Username: "minhnn", DisplayName: "Minh", Color: "#FF8A8A", Badges: []string{"STREAMER"}},
		Fragments: []entity.Fragment{{Type: "text", Text: "gg"}},
	}}, &next, nil
}

func (*fakeChat) Settings(_ context.Context, id string) (entity.ChatSettings, error) {
	if id != channelID {
		return entity.ChatSettings{}, entity.ErrNotFound
	}

	return entity.ChatSettings{ChannelID: id, SlowSec: 5}, nil
}

func (f *fakeChat) UpdateSettings(_ context.Context, p entity.Principal, s entity.ChatSettings) (entity.ChatSettings, error) {
	if p.UserID != "owner" {
		return entity.ChatSettings{}, entity.ErrForbidden
	}

	f.updatedBy = p.UserID
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s.UpdatedAt = &at

	return s, nil
}

func (*fakeChat) Emotes(context.Context) ([]entity.Emote, error) {
	return []entity.Emote{{ID: "e1", Code: "nxGG", ImageURL: "/emotes/nxGG.webp", Scope: "GLOBAL"}}, nil
}

func newApp(c usecase.Chat) *fiber.App {
	app := fiber.New()
	// Stands in for middleware.Auth: the "X-User" header becomes the authenticated user id.
	auth := func(ctx *fiber.Ctx) error {
		if u := ctx.Get("X-User"); u != "" {
			ctx.Locals("userID", u)

			return ctx.Next()
		}

		return ctx.Status(http.StatusUnauthorized).JSON(fiber.Map{"error": fiber.Map{"code": "UNAUTHORIZED"}})
	}

	v1.NewRoutes(app.Group("/v1"), c, auth, logger.New("error"))

	return app
}

func do(t *testing.T, app *fiber.App, method, path, body, user string) (status int, out map[string]any) {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	if user != "" {
		req.Header.Set("X-User", user)
	}

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.NoError(t, json.Unmarshal(raw, &out), string(raw))

	return resp.StatusCode, out
}

func TestHistory(t *testing.T) {
	t.Parallel()

	c := &fakeChat{}
	app := newApp(c)

	status, body := do(t, app, http.MethodGet, "/v1/chat/rooms/"+streamID+"/messages?before=0199d4a2-0000-7000-8000-000000000009&limit=20", "", "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, 20, c.gotLimit)
	require.Equal(t, "0199d4a2-0000-7000-8000-000000000009", *c.gotBefore)

	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "m1", data["nextCursor"])

	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)

	m, ok := items[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "2026-10-14T12:00:00Z", m["createdAt"])
	require.Equal(t, []any{map[string]any{"type": "text", "text": "gg"}}, m["fragments"])

	for _, tc := range []struct {
		path   string
		status int
		code   string
	}{
		{path: "/v1/chat/rooms/" + streamID + "/messages?limit=101", status: http.StatusBadRequest, code: "VALIDATION_FAILED"},
		{path: "/v1/chat/rooms/" + streamID + "/messages?before=x", status: http.StatusBadRequest, code: "VALIDATION_FAILED"},
		{path: "/v1/chat/rooms/not-a-uuid/messages", status: http.StatusNotFound, code: "NOT_FOUND"},
		{path: "/v1/chat/rooms/0199d4a2-0000-7000-8000-0000000000ff/messages", status: http.StatusNotFound, code: "NOT_FOUND"},
	} {
		status, body = do(t, app, http.MethodGet, tc.path, "", "")
		require.Equal(t, tc.status, status, tc.path)

		e, ok := body["error"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, tc.code, e["code"], tc.path)
	}
}

func TestSettingsRoutes(t *testing.T) {
	t.Parallel()

	c := &fakeChat{}
	app := newApp(c)
	path := "/v1/channels/" + channelID + "/chat-settings"

	status, body := do(t, app, http.MethodGet, path, "", "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, map[string]any{"slowSec": float64(5), "followersMin": nil, "subsOnly": false, "updatedAt": nil}, body["data"])

	status, _ = do(t, app, http.MethodGet, "/v1/channels/0199d4a2-0000-7000-8000-0000000000ff/chat-settings", "", "")
	require.Equal(t, http.StatusNotFound, status)

	tests := []struct {
		name    string
		body    string
		user    string
		status  int
		code    string
		details map[string]any
	}{
		{name: "guest", body: `{"slowSec":5}`, status: http.StatusUnauthorized, code: "UNAUTHORIZED"},
		{
			name: "bad enums", body: `{"slowSec":3,"followersMin":7}`, user: "owner", status: http.StatusBadRequest,
			code: "VALIDATION_FAILED", details: map[string]any{"slowSec": "ENUM", "followersMin": "ENUM"},
		},
		{name: "not json", body: `{`, user: "owner", status: http.StatusBadRequest, code: "VALIDATION_FAILED"},
		{name: "not allowed", body: `{"slowSec":5}`, user: "viewer", status: http.StatusForbidden, code: "FORBIDDEN"},
		{name: "owner", body: `{"slowSec":10,"followersMin":0,"subsOnly":true}`, user: "owner", status: http.StatusOK},
	}

	for _, tc := range tests {
		status, body = do(t, app, http.MethodPut, path, tc.body, tc.user)
		require.Equal(t, tc.status, status, tc.name)

		if tc.code != "" {
			e, ok := body["error"].(map[string]any)
			require.True(t, ok, tc.name)
			require.Equal(t, tc.code, e["code"], tc.name)

			if tc.details != nil {
				require.Equal(t, tc.details, e["details"], tc.name)
			}

			continue
		}

		require.Equal(t, map[string]any{"slowSec": float64(10), "followersMin": float64(0), "subsOnly": true, "updatedAt": "2026-10-14T12:00:00Z"}, body["data"])
	}

	require.Equal(t, "owner", c.updatedBy)
}

func TestEmotes(t *testing.T) {
	t.Parallel()

	status, body := do(t, newApp(&fakeChat{}), http.MethodGet, "/v1/chat/emotes?channelId=x", "", "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, map[string]any{"items": []any{map[string]any{
		"id": "e1", "code": "nxGG", "imageUrl": "/emotes/nxGG.webp", "animated": false, "scope": "GLOBAL",
	}}}, body["data"])
}

// The REST message and the WebSocket `message` event data must have the same JSON shape.
func TestMessageShapeMatchesRealtimeEvent(t *testing.T) {
	t.Parallel()

	_, body := do(t, newApp(&fakeChat{}), http.MethodGet, "/v1/chat/rooms/"+streamID+"/messages", "", "")

	data, ok := body["data"].(map[string]any)
	require.True(t, ok)

	items, ok := data["items"].([]any)
	require.True(t, ok)

	msgs, _, err := (&fakeChat{}).History(context.Background(), streamID, nil, 1)
	require.NoError(t, err)

	raw, err := json.Marshal(msgs[0])
	require.NoError(t, err)

	var event map[string]any
	require.NoError(t, json.Unmarshal(raw, &event))
	require.Equal(t, event, items[0])
}
