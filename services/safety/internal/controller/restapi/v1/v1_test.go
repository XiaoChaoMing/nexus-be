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
	"github.com/minhnn/nexuslive-be/services/safety/internal/controller/restapi/middleware"
	v1 "github.com/minhnn/nexuslive-be/services/safety/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
	"github.com/stretchr/testify/require"
)

type fakeUC struct {
	usecase.AdminReports
	usecase.Directory

	fileErr error
	created bool
	gotLock usecase.LockInput
}

func (f *fakeUC) File(context.Context, string, usecase.ReportInput) (entity.Report, bool, error) {
	return entity.Report{ID: "r1", Status: entity.ReportOpen}, f.created, f.fileErr
}

func (f *fakeUC) LockUser(_ context.Context, _, userID string, in usecase.LockInput) (string, error) {
	f.gotLock = in

	if userID == "admin" {
		return "", entity.ErrForbidden
	}

	return "a1", nil
}

func (f *fakeUC) UnlockUser(context.Context, string, string, usecase.ActionInput) (string, error) {
	return "", entity.Reject(entity.ErrValidation, "reason", "LENGTH")
}

func (f *fakeUC) SuspendChannel(context.Context, string, string, usecase.ActionInput) (string, error) {
	return "", entity.ErrNotFound
}

func (f *fakeUC) RestoreChannel(context.Context, string, string, usecase.ActionInput) (string, error) {
	return "a2", nil
}

func (f *fakeUC) TakedownStream(context.Context, string, string, usecase.ActionInput) (string, error) {
	return "", entity.ErrInvalidState
}

type fakeAudit struct{ usecase.Audit }

// as authenticates every request as id with role (middleware.Auth stores the same Locals).
func as(id, role string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", id)
		ctx.Locals("role", role)

		return ctx.Next()
	}
}

func app(uc *fakeUC, role string) *fiber.App {
	a := fiber.New()
	v1.NewRoutes(a.Group("/v1"), v1.UseCases{Reports: uc, Queue: uc, Actions: uc, Directory: uc, Audit: fakeAudit{}},
		as("u1", role), middleware.RequireAdmin(), logger.New("error"))

	return a
}

type result struct {
	status int
	header http.Header
	body   map[string]any
}

func call(t *testing.T, a *fiber.App, method, path, body string) result {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.Test(req)
	require.NoError(t, err)

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	out := map[string]any{}
	if len(b) > 0 {
		require.NoError(t, json.Unmarshal(b, &out))
	}

	return result{status: resp.StatusCode, header: resp.Header, body: out}
}

func TestReports(t *testing.T) {
	t.Parallel()

	uc := &fakeUC{created: true}
	res := call(t, app(uc, "USER"), http.MethodPost, "/v1/reports", `{"targetType":"USER","targetId":"x","reason":"SPAM"}`)
	require.Equal(t, http.StatusCreated, res.status)

	uc.created = false
	res = call(t, app(uc, "USER"), http.MethodPost, "/v1/reports", `{}`)
	require.Equal(t, http.StatusOK, res.status)

	uc.fileErr = &entity.RateLimitError{RetryAfter: 1500 * time.Millisecond}
	res = call(t, app(uc, "USER"), http.MethodPost, "/v1/reports", `{}`)
	require.Equal(t, http.StatusTooManyRequests, res.status)
	require.Equal(t, "2", res.header.Get("Retry-After"))
	require.Equal(t, map[string]any{
		"code": "RATE_LIMITED", "message": "too many reports",
		"details": map[string]any{"retryAfterSec": "2"},
	}, res.body["error"])

	res = call(t, app(uc, "USER"), http.MethodPost, "/v1/reports", `not json`)
	require.Equal(t, http.StatusBadRequest, res.status)
}

func TestAdminActions(t *testing.T) {
	t.Parallel()

	uc := &fakeUC{}

	res := call(t, app(uc, "USER"), http.MethodPost, "/v1/admin/users/x/lock", `{"reason":"long enough reason"}`)
	require.Equal(t, http.StatusForbidden, res.status, "not an admin")

	a := app(uc, "ADMIN")

	tests := []struct {
		path   string
		status int
		code   string
	}{
		{"/v1/admin/users/x/lock", http.StatusAccepted, ""},
		{"/v1/admin/users/admin/lock", http.StatusForbidden, "FORBIDDEN"},
		{"/v1/admin/users/x/unlock", http.StatusBadRequest, "VALIDATION_FAILED"},
		{"/v1/admin/channels/x/suspend", http.StatusNotFound, "NOT_FOUND"},
		{"/v1/admin/channels/x/restore", http.StatusAccepted, ""},
		{"/v1/admin/streams/x/takedown", http.StatusConflict, "INVALID_STATE"},
	}

	for _, tc := range tests {
		res := call(t, a, http.MethodPost, tc.path, `{"durationHours":168,"reason":"long enough reason"}`)
		require.Equal(t, tc.status, res.status, tc.path)

		if tc.code != "" {
			require.Equal(t, map[string]any{"code": tc.code}, pick(res.body["error"], "code"), tc.path)
		} else {
			require.NotEmpty(t, pick(res.body["data"], "actionId")["actionId"], tc.path)
		}
	}

	require.Equal(t, 168, *uc.gotLock.DurationHours)

	require.Equal(t, http.StatusBadRequest, call(t, a, http.MethodGet, "/v1/admin/reports?limit=0", "").status)
	require.Equal(t, http.StatusBadRequest, call(t, a, http.MethodGet, "/v1/admin/audit-log?from=yesterday", "").status)
}

// pick keeps one key of a JSON object.
func pick(v any, key string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	return map[string]any{key: m[key]}
}
