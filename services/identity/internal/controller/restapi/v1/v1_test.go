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
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	v1 "github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _userID = "0190a000-0000-7000-8000-000000000001"

// fakeAuth returns err from every call it implements; the rest of usecase.Auth is not exercised.
type fakeAuth struct {
	usecase.Auth

	err     error
	profile usecase.Profile
}

func (f fakeAuth) Register(context.Context, usecase.RegisterInput) (usecase.AuthResult, error) {
	return usecase.AuthResult{}, f.err
}

func (f fakeAuth) Login(context.Context, usecase.LoginInput) (usecase.AuthResult, error) {
	return usecase.AuthResult{}, f.err
}

func (f fakeAuth) ResendVerification(context.Context, string) error { return f.err }

func (f fakeAuth) Profile(context.Context, string) (usecase.Profile, error) { return f.profile, f.err }

func (f fakeAuth) UpdateProfile(context.Context, string, usecase.ProfileUpdate) (usecase.Profile, error) {
	return f.profile, f.err
}

type fakeCreator struct {
	usecase.Creator

	mine *entity.Application
}

func (f fakeCreator) Mine(context.Context, string) (*entity.Application, error) { return f.mine, nil }

type result struct {
	status int
	header http.Header
	body   map[string]any
}

func call(t *testing.T, a usecase.Auth, c usecase.Creator, method, path, body string) result {
	t.Helper()

	signer, err := jwt.GenerateEdDSA("test", time.Minute)
	require.NoError(t, err)

	app := fiber.New()
	v1.NewRoutes(app.Group("/v1"), v1.Deps{Auth: a, Creator: c, Signer: signer, Logger: logger.New("error")})

	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)

	token, err := signer.Sign(_userID, entity.RoleUser, "sid", time.Now())
	require.NoError(t, err)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	out := result{status: resp.StatusCode, header: resp.Header}
	if len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out.body), string(raw))
	}

	return out
}

func errorOf(r result) (code string, details map[string]any) {
	e, _ := r.body["error"].(map[string]any)   //nolint:errcheck // asserted by the caller
	code, _ = e["code"].(string)               //nolint:errcheck // same
	details, _ = e["details"].(map[string]any) //nolint:errcheck // same

	return code, details
}

func TestErrorContract(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	until := time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC)
	register := `{"username":"viewer_1","email":"v1@example.com","password":"secret123","acceptTerms":true}`

	tests := []struct {
		name        string
		err         error
		method      string
		path        string
		body        string
		wantStatus  int
		wantCode    string
		wantDetails map[string]any
		wantHeader  [2]string
	}{
		{
			name: "register without acceptTerms", method: http.MethodPost, path: "/v1/auth/register",
			body:       `{"username":"viewer_1","email":"v1@example.com","password":"secret123"}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_FAILED", wantDetails: map[string]any{"acceptTerms": "REQUIRED"},
		},
		{
			name: "username taken", err: entity.ErrUsernameTaken, method: http.MethodPost, path: "/v1/auth/register", body: register,
			wantStatus: http.StatusConflict, wantCode: "USERNAME_TAKEN", wantDetails: map[string]any{"username": "TAKEN"},
		},
		{
			name: "email taken", err: entity.ErrEmailTaken, method: http.MethodPost, path: "/v1/auth/register", body: register,
			wantStatus: http.StatusConflict, wantCode: "EMAIL_TAKEN", wantDetails: map[string]any{"email": "TAKEN"},
		},
		{
			name: "temporary lock", err: &entity.LockedError{Until: until}, method: http.MethodPost, path: "/v1/auth/login",
			body:       `{"login":"viewer_1","password":"secret123"}`,
			wantStatus: http.StatusForbidden, wantCode: "ACCOUNT_LOCKED", wantDetails: map[string]any{"lockedUntil": "2026-10-13T10:15:00Z"},
		},
		{
			name: "resend too fast", err: &entity.RetryError{RetryAfter: 41500 * time.Millisecond}, method: http.MethodPost,
			path: "/v1/auth/verify-email/resend", wantStatus: http.StatusTooManyRequests, wantCode: "RATE_LIMITED",
			wantDetails: map[string]any{"retryAfterSec": "42"}, wantHeader: [2]string{"Retry-After", "42"},
		},
		{
			name: "resend when verified", err: entity.ErrAlreadyVerified, method: http.MethodPost, path: "/v1/auth/verify-email/resend",
			wantStatus: http.StatusConflict, wantCode: "CONFLICT",
		},
		{
			name: "refresh without client header", method: http.MethodPost, path: "/v1/auth/refresh",
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN", wantDetails: map[string]any{"header": "REQUIRED"},
		},
		{
			name: "admin route as a viewer", method: http.MethodGet, path: "/v1/admin/creator-applications",
			wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN",
		},
		{
			name: "patch me with a bad timezone", method: http.MethodPatch, path: "/v1/me", body: `{"timezone":"Mars/Base"}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_FAILED", wantDetails: map[string]any{"timezone": "TIMEZONE"},
		},
		{
			name: "patch me with a blank display name", method: http.MethodPatch, path: "/v1/me", body: `{"displayName":"   "}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_FAILED", wantDetails: map[string]any{"displayName": "MIN"},
		},
		{
			name: "birth date already set", err: entity.ErrBirthDateAlreadySet, method: http.MethodPatch, path: "/v1/me",
			body: `{"birthDate":"2000-01-31"}`, wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_FAILED",
			wantDetails: map[string]any{"birthDate": "ALREADY_SET"},
		},
		{
			name: "cancel with a malformed id", method: http.MethodDelete, path: "/v1/creator-applications/not-a-uuid",
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := call(t, fakeAuth{err: tc.err}, fakeCreator{}, tc.method, tc.path, tc.body)
			assert.Equal(t, tc.wantStatus, r.status)

			code, details := errorOf(r)
			assert.Equal(t, tc.wantCode, code)

			for k, v := range tc.wantDetails {
				assert.Equal(t, v, details[k], k)
			}

			if tc.wantHeader[0] != "" {
				assert.Equal(t, tc.wantHeader[1], r.header.Get(tc.wantHeader[0]))
			}
		})
	}
}

func TestGetMe(t *testing.T) {
	t.Parallel()

	channel := "0190a000-0000-7000-8000-0000000000cc"
	birth := time.Date(2000, 1, 31, 0, 0, 0, 0, time.UTC)
	profile := usecase.Profile{User: entity.User{
		ID: _userID, Username: "minhnn", DisplayName: "Minh", GlobalRole: entity.RoleAdmin, BirthDate: &birth,
		Locale: "vi", Timezone: "Asia/Ho_Chi_Minh", ChannelID: &channel, Preferences: entity.Preferences{MatureVisible: true},
	}}

	r := call(t, fakeAuth{profile: profile}, fakeCreator{}, http.MethodGet, "/v1/me", "")
	require.Equal(t, http.StatusOK, r.status)

	data, ok := r.body["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"VIEWER", "CREATOR", "ADMIN"}, data["roles"])
	assert.Equal(t, true, data["isCreator"])
	assert.Equal(t, channel, data["channelId"])
	assert.InDelta(t, 0, data["unreadNotifications"], 0)
	assert.Equal(t, map[string]any{"sidebarCollapsed": false, "matureVisible": true}, data["preferences"])

	user, ok := data["user"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "2000-01-31", user["birthDate"])
	assert.Equal(t, true, user["isCreator"])
	assert.Equal(t, "Asia/Ho_Chi_Minh", user["timezone"])
}

func TestMyApplication_None(t *testing.T) {
	t.Parallel()

	r := call(t, fakeAuth{}, fakeCreator{}, http.MethodGet, "/v1/creator-applications/me", "")
	require.Equal(t, http.StatusOK, r.status)

	data, present := r.body["data"]
	assert.True(t, present)
	assert.Nil(t, data)
}
