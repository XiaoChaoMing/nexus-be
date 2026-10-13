package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errorBody struct {
	Error struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func do(t *testing.T, app *fiber.App, req *http.Request) (int, errorBody) {
	t.Helper()

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	var body errorBody
	if resp.StatusCode != http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	}

	return resp.StatusCode, body
}

func TestRequireClient(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Post("/refresh", middleware.RequireClient(), func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{name: "web", header: "web", want: http.StatusOK},
		{name: "admin", header: "admin", want: http.StatusOK},
		{name: "missing", header: "", want: http.StatusForbidden},
		{name: "other value", header: "curl", want: http.StatusForbidden},
		{name: "wrong case", header: "WEB", want: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/refresh", http.NoBody)
			if tc.header != "" {
				req.Header.Set(middleware.ClientHeader, tc.header)
			}

			status, body := do(t, app, req)
			assert.Equal(t, tc.want, status)

			if tc.want == http.StatusForbidden {
				assert.Equal(t, "FORBIDDEN", body.Error.Code)
				assert.Equal(t, "REQUIRED", body.Error.Details["header"])
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	t.Parallel()

	signer, err := jwt.GenerateEdDSA("test", time.Minute)
	require.NoError(t, err)

	app := fiber.New()
	app.Get("/admin", middleware.Auth(signer), middleware.RequireAdmin(), func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })

	token := func(role string) string {
		s, serr := signer.Sign("0190a000-0000-7000-8000-000000000001", role, "sid", time.Now())
		require.NoError(t, serr)

		return "Bearer " + s
	}

	tests := []struct {
		name     string
		auth     string
		want     int
		wantCode string
	}{
		{name: "admin", auth: token("ADMIN"), want: http.StatusOK},
		{name: "user", auth: token("USER"), want: http.StatusForbidden, wantCode: "FORBIDDEN"},
		{name: "no token", auth: "", want: http.StatusUnauthorized, wantCode: "UNAUTHORIZED"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin", http.NoBody)
			if tc.auth != "" {
				req.Header.Set(fiber.HeaderAuthorization, tc.auth)
			}

			status, body := do(t, app, req)
			assert.Equal(t, tc.want, status)
			assert.Equal(t, tc.wantCode, body.Error.Code)
		})
	}
}
