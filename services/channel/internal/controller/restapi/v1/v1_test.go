package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	v1 "github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

const (
	_channelID = "0199c0de-0000-7000-8000-000000000001"
	_userID    = "0199c0de-0000-7000-8000-0000000000aa"
)

type fakeUC struct {
	usecase.Directory
	usecase.Follows
	usecase.Studio
	usecase.Catalog
	usecase.Schedules
	usecase.Reminders

	gotImage []byte
	gotSched entity.ScheduleInput
}

func (fakeUC) Profile(_ context.Context, handle, _ string) (entity.Profile, error) {
	switch handle {
	case "minhnn":
		return entity.Profile{Channel: entity.Channel{
			ID: "c1", Handle: "minhnn", DisplayName: "Minh", Status: entity.StatusActive,
			CreatedAt: time.Unix(0, 0),
		}}, nil
	case "banned":
		return entity.Profile{}, entity.ErrSuspended
	case "broken":
		return entity.Profile{}, errDB
	}

	return entity.Profile{}, entity.ErrNotFound
}

func (fakeUC) Follow(_ context.Context, userID, channelID string) (entity.Follow, error) {
	if userID == channelID {
		return entity.Follow{}, entity.ErrCannotFollowSelf
	}

	return entity.Follow{UserID: userID, ChannelID: channelID, NotifyLevel: entity.NotifyAll}, nil
}

func (fakeUC) UpdateProfile(_ context.Context, _ string, p entity.ProfilePatch) (entity.Channel, error) {
	if p.Handle != nil && *p.Handle == "taken" {
		return entity.Channel{}, entity.Reject(entity.ErrHandleTaken, "handle", entity.DetailTaken)
	}

	if p.Handle != nil {
		return entity.Channel{}, entity.Reject(entity.ErrHandleTooSoon, "nextChangeAt", "2026-11-14T00:00:00Z")
	}

	return entity.Channel{ID: "c1"}, nil
}

func (fakeUC) NextHandleChangeAt(entity.Channel) *time.Time { return nil }

func (f *fakeUC) SetImage(_ context.Context, _ string, _ entity.ImageKind, data []byte) (string, error) {
	f.gotImage = data

	return "/media/channels/c1/avatar-1.jpg", nil
}

func (f *fakeUC) Create(_ context.Context, _ string, in entity.ScheduleInput) (entity.ScheduleView, error) {
	f.gotSched = in

	return entity.ScheduleView{}, entity.Reject(entity.ErrScheduleLimit, "limit", "20")
}

func (fakeUC) Suggest(context.Context, string, []entity.DocType, int) ([]entity.SearchGroup, error) {
	return []entity.SearchGroup{{Type: entity.DocCategory, Items: []entity.SearchDoc{{Type: entity.DocCategory, ID: "x", Viewers: 3}}}}, nil
}

func (fakeUC) Search(context.Context, string, entity.DocType, string, int) (usecase.SearchPage, error) {
	return usecase.SearchPage{}, nil
}

func (fakeUC) History(context.Context, string) ([]entity.SearchQuery, error) { return nil, nil }

func (fakeUC) Remember(context.Context, string, string) (entity.SearchQuery, error) {
	return entity.SearchQuery{}, nil
}

func (fakeUC) Forget(context.Context, string, *string) error { return nil }

func newApp(uc *fakeUC) *fiber.App {
	app := fiber.New()
	// Tests authenticate with a header holding the user id instead of a JWT.
	auth := func(ctx *fiber.Ctx) error {
		if id := ctx.Get("X-Test-User"); id != "" {
			ctx.Locals("userID", id)

			return ctx.Next()
		}

		return ctx.Status(http.StatusUnauthorized).JSON(fiber.Map{"error": fiber.Map{"code": "UNAUTHORIZED"}})
	}
	v1.NewRoutes(app.Group("/v1"), v1.UseCases{
		Directory: uc, Follows: uc, Studio: uc, Catalog: uc, Search: uc, Schedules: uc, Reminders: uc,
	}, v1.Auth{Required: auth, Optional: func(ctx *fiber.Ctx) error {
		if ctx.Get("X-Test-User") != "" {
			return auth(ctx)
		}

		return ctx.Next()
	}}, v1.Options{AvatarMaxBytes: 1024, BannerMaxBytes: 2048, ScheduleWindow: 14 * 24 * time.Hour, ScheduleHorizon: 90 * 24 * time.Hour},
		logger.New("error"))

	return app
}

type call struct {
	name, method, path, user, body, ctype string
	raw                                   []byte
	status                                int
	code                                  string
	details                               map[string]string
}

func run(t *testing.T, app *fiber.App, c call) []byte {
	t.Helper()

	body := io.Reader(http.NoBody)

	switch {
	case c.raw != nil:
		body = bytes.NewReader(c.raw)
	case c.body != "":
		body = strings.NewReader(c.body)
	}

	req := httptest.NewRequestWithContext(context.Background(), c.method, c.path, body)
	if c.user != "" {
		req.Header.Set("X-Test-User", c.user)
	}

	switch {
	case c.ctype != "":
		req.Header.Set(fiber.HeaderContentType, c.ctype)
	case c.body != "":
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}

	resp, err := app.Test(req)
	require.NoError(t, err)

	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, c.status, resp.StatusCode, "%s: %s", c.name, out)

	if c.code != "" {
		var e response.Error
		require.NoError(t, json.Unmarshal(out, &e))
		require.Equal(t, c.code, e.Error.Code, c.name)
		require.NotContains(t, string(out), "db down", "no internal details leak")

		if c.details != nil {
			require.Equal(t, c.details, e.Error.Details, c.name)
		}
	}

	return out
}

func multipartFile(t *testing.T, field string, data []byte) (body []byte, contentType string) {
	t.Helper()

	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(field, "x.jpg")
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	return buf.Bytes(), w.FormDataContentType()
}

func TestRoutes(t *testing.T) { //nolint:funlen // one table of routes
	t.Parallel()

	uc := &fakeUC{}
	app := newApp(uc)
	small, smallType := multipartFile(t, "file", bytes.Repeat([]byte{1}, 10))
	big, bigType := multipartFile(t, "file", bytes.Repeat([]byte{1}, 1025))
	noFile, noFileType := multipartFile(t, "other", []byte{1})

	tests := []call{
		{name: "profile", method: http.MethodGet, path: "/v1/channels/minhnn", status: http.StatusOK},
		{name: "profile unknown", method: http.MethodGet, path: "/v1/channels/nobody", status: http.StatusNotFound, code: entity.CodeNotFound},
		{name: "profile suspended", method: http.MethodGet, path: "/v1/channels/banned", status: http.StatusForbidden, code: entity.CodeChannelSuspended},
		{name: "profile db down", method: http.MethodGet, path: "/v1/channels/broken", status: http.StatusInternalServerError, code: entity.CodeInternal},
		{name: "follow guest", method: http.MethodPost, path: "/v1/channels/" + _channelID + "/follow", status: http.StatusUnauthorized, code: entity.CodeUnauthorized},
		{name: "follow", method: http.MethodPost, path: "/v1/channels/" + _channelID + "/follow", user: _userID, status: http.StatusOK},
		{name: "follow self", method: http.MethodPost, path: "/v1/channels/" + _userID + "/follow", user: _userID, status: http.StatusUnprocessableEntity, code: entity.CodeCannotFollowSelf},
		{name: "follow bad id", method: http.MethodPost, path: "/v1/channels/nope/follow", user: _userID, status: http.StatusNotFound, code: entity.CodeNotFound},
		{
			name: "bell enum", method: http.MethodPut, path: "/v1/channels/" + _channelID + "/follow", user: _userID, body: `{"notifyLevel":"NONE"}`,
			status: http.StatusBadRequest, code: entity.CodeValidationFailed, details: map[string]string{"notifyLevel": entity.DetailEnum},
		},
		{name: "following status", method: http.MethodGet, path: "/v1/me/following?status=x", user: _userID, status: http.StatusBadRequest, code: entity.CodeValidationFailed},
		{name: "sidebar limit", method: http.MethodGet, path: "/v1/me/following/sidebar?limit=51", user: _userID, status: http.StatusBadRequest, code: entity.CodeValidationFailed},
		{
			name: "categories group", method: http.MethodGet, path: "/v1/categories?group=SPORTS", status: http.StatusBadRequest,
			code: entity.CodeValidationFailed, details: map[string]string{"group": entity.DetailEnum},
		},
		{name: "suggest", method: http.MethodGet, path: "/v1/search/suggest?q=lien+minh", status: http.StatusOK},
		{
			name: "suggest types", method: http.MethodGet, path: "/v1/search/suggest?q=ab&types=tag", status: http.StatusBadRequest,
			code: entity.CodeValidationFailed, details: map[string]string{"types": entity.DetailEnum},
		},
		{
			name: "search type", method: http.MethodGet, path: "/v1/search?q=ab", status: http.StatusBadRequest,
			code: entity.CodeValidationFailed, details: map[string]string{"type": entity.DetailRequired},
		},
		{
			name: "schedule range", method: http.MethodGet, path: "/v1/channels/" + _channelID + "/schedule?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z",
			status: http.StatusBadRequest, code: entity.CodeValidationFailed, details: map[string]string{"to": entity.DetailMax},
		},
		{
			name: "remind format", method: http.MethodPost, path: "/v1/schedules/" + _channelID + "/reminders", user: _userID, body: `{"occurrenceAt":"tomorrow"}`,
			status: http.StatusBadRequest, code: entity.CodeValidationFailed, details: map[string]string{"occurrenceAt": entity.DetailFormat},
		},
		{name: "studio guest", method: http.MethodGet, path: "/v1/studio/channel", status: http.StatusUnauthorized, code: entity.CodeUnauthorized},
		{
			name: "handle taken", method: http.MethodPatch, path: "/v1/studio/channel", user: _userID, body: `{"handle":"taken"}`,
			status: http.StatusConflict, code: entity.CodeHandleTaken, details: map[string]string{"handle": entity.DetailTaken},
		},
		{
			name: "handle too soon", method: http.MethodPatch, path: "/v1/studio/channel", user: _userID, body: `{"handle":"other"}`,
			status: http.StatusConflict, code: entity.CodeConflict, details: map[string]string{"nextChangeAt": "2026-11-14T00:00:00Z"},
		},
		{
			name: "patch not json", method: http.MethodPatch, path: "/v1/studio/channel", user: _userID, body: `[1`,
			status: http.StatusBadRequest, code: entity.CodeValidationFailed,
		},
		{name: "avatar", method: http.MethodPut, path: "/v1/studio/channel/avatar", user: _userID, raw: small, ctype: smallType, status: http.StatusOK},
		{
			name: "avatar too large", method: http.MethodPut, path: "/v1/studio/channel/avatar", user: _userID, raw: big, ctype: bigType,
			status: http.StatusRequestEntityTooLarge, code: entity.CodePayloadTooLarge,
		},
		{name: "banner fits its own limit", method: http.MethodPut, path: "/v1/studio/channel/banner", user: _userID, raw: big, ctype: bigType, status: http.StatusOK},
		{
			name: "avatar without file", method: http.MethodPut, path: "/v1/studio/channel/avatar", user: _userID, raw: noFile, ctype: noFileType,
			status: http.StatusBadRequest, code: entity.CodeValidationFailed, details: map[string]string{"file": entity.DetailRequired},
		},
		{
			name: "schedule start format", method: http.MethodPost, path: "/v1/studio/schedules", user: _userID, body: `{"title":"x","startAt":"20:00"}`,
			status: http.StatusBadRequest, code: entity.CodeValidationFailed, details: map[string]string{"startAt": entity.DetailFormat},
		},
		{
			name: "schedule limit", method: http.MethodPost, path: "/v1/studio/schedules", user: _userID,
			body:   `{"title":"x","startAt":"2026-10-20T20:00:00+07:00","durationMin":60,"rrule":"FREQ=WEEKLY;BYDAY=TU"}`,
			status: http.StatusConflict, code: entity.CodeConflict, details: map[string]string{"limit": "20"},
		},
		{
			name: "occurrence path", method: http.MethodDelete, path: "/v1/studio/schedules/" + _channelID + "/occurrences/yesterday", user: _userID,
			status: http.StatusNotFound, code: entity.CodeNotFound,
		},
	}

	// Sequential on purpose: the checks below read what the fake recorded.
	for _, tc := range tests {
		run(t, app, tc)
	}

	require.Len(t, uc.gotImage, 1025)
	require.Equal(t, "FREQ=WEEKLY;BYDAY=TU", uc.gotSched.RRule)
	require.Equal(t, time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC), uc.gotSched.StartAt)
}

func TestSuggestBody(t *testing.T) {
	t.Parallel()

	out := run(t, newApp(&fakeUC{}), call{name: "suggest", method: http.MethodGet, path: "/v1/search/suggest?q=+lien+minh+", status: http.StatusOK})
	require.JSONEq(t, `{"data":{"query":"lien minh","groups":[{"type":"category","items":[{"type":"category","id":"x","title":"",
		"subtitle":null,"imageUrl":null,"viewerCount":3,"isLive":false,"isMature":false,"target":""}]}]}}`, string(out))
}
