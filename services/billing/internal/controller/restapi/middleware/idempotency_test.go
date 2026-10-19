package middleware_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/require"
)

const key = "0199f0a3-0000-7000-8000-0000000000aa"

// memIdem keeps keys like the idempotency_keys table.
type memIdem struct {
	mu   sync.Mutex
	rows map[string]*row
}

type row struct {
	hash   string
	status int
	body   []byte
}

func (m *memIdem) Begin(_ context.Context, k usecase.IdemKey, hash []byte) (usecase.IdemState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := k.UserID + k.Method + k.Path + k.Key

	r, ok := m.rows[id]
	switch {
	case !ok:
		m.rows[id] = &row{hash: string(hash)}

		return usecase.IdemState{Outcome: usecase.IdemNew}, nil
	case r.hash != string(hash):
		return usecase.IdemState{Outcome: usecase.IdemMismatch}, nil
	case r.status == 0:
		return usecase.IdemState{Outcome: usecase.IdemInProgress}, nil
	}

	return usecase.IdemState{Outcome: usecase.IdemDone, Status: r.status, Body: r.body}, nil
}

func (m *memIdem) Complete(_ context.Context, k usecase.IdemKey, status int, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r := m.rows[k.UserID+k.Method+k.Path+k.Key]
	r.status, r.body = status, body

	return nil
}

func (m *memIdem) Abort(_ context.Context, k usecase.IdemKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.rows, k.UserID+k.Method+k.Path+k.Key)

	return nil
}

func TestIdempotency(t *testing.T) { //nolint:funlen // one story
	t.Parallel()

	calls := 0
	idem := &memIdem{rows: map[string]*row{}}
	app := fiber.New()
	app.Post("/orders", func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "u1")

		return ctx.Next()
	}, middleware.Idempotency(idem, logger.New("error")), func(ctx *fiber.Ctx) error {
		calls++

		if strings.Contains(string(ctx.Body()), "fail") {
			return ctx.Status(http.StatusConflict).JSON(fiber.Map{"error": "x"})
		}

		return ctx.Status(http.StatusCreated).JSON(fiber.Map{"data": calls})
	})

	do := func(k, body string) (int, string, string) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/orders", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		if k != "" {
			req.Header.Set(middleware.HeaderIdempotencyKey, k)
		}

		resp, err := app.Test(req)
		require.NoError(t, err)

		defer resp.Body.Close()

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		return resp.StatusCode, string(b), resp.Header.Get("Idempotent-Replay")
	}

	status, body, _ := do("", `{}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, `"idempotencyKey":"REQUIRED"`)

	status, body, _ = do("abc", `{}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, `"idempotencyKey":"FORMAT"`)

	status, body, replay := do(key, `{"tierId":"a","x":1}`)
	require.Equal(t, http.StatusCreated, status)
	require.JSONEq(t, `{"data":1}`, body)
	require.Empty(t, replay)

	// Same key and body (keys reordered, spaces): the stored answer, the handler does not run.
	status, body, replay = do(key, `{ "x":1, "tierId":"a" }`)
	require.Equal(t, http.StatusCreated, status)
	require.JSONEq(t, `{"data":1}`, body)
	require.Equal(t, "true", replay)
	require.Equal(t, 1, calls)

	status, body, _ = do(key, `{"tierId":"b"}`)
	require.Equal(t, http.StatusUnprocessableEntity, status)
	require.Contains(t, body, "IDEMPOTENCY_KEY_REUSED")

	// An error answer is not stored: the same key retries.
	other := "0199f0a3-0000-7000-8000-0000000000bb"
	status, _, _ = do(other, `{"fail":true}`)
	require.Equal(t, http.StatusConflict, status)
	status, _, _ = do(other, `{"fail":true}`)
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, 3, calls)

	// A key still running.
	running, _ := middleware.CanonicalHash([]byte(`{}`))
	idem.rows["u1POST/orders0199f0a3-0000-7000-8000-0000000000cc"] = &row{hash: string(running)}
	status, body, _ = do("0199f0a3-0000-7000-8000-0000000000cc", `{}`)
	require.Equal(t, http.StatusConflict, status)
	require.Contains(t, body, `"code":"CONFLICT"`)
}

func TestCanonicalHash(t *testing.T) {
	t.Parallel()

	a, ok := middleware.CanonicalHash([]byte(`{"b":1,"a":[1,2]}`))
	require.True(t, ok)

	b, ok := middleware.CanonicalHash([]byte(" {\n \"a\": [1, 2], \"b\": 1 } "))
	require.True(t, ok)
	require.Equal(t, a, b)

	c, ok := middleware.CanonicalHash([]byte(`{"b":1.0,"a":[1,2]}`))
	require.True(t, ok)
	require.NotEqual(t, a, c, "numbers keep their literal")

	empty, ok := middleware.CanonicalHash(nil)
	require.True(t, ok)
	require.Len(t, empty, 32)

	_, ok = middleware.CanonicalHash([]byte(`{"a":`))
	require.False(t, ok)

	_, ok = middleware.CanonicalHash([]byte(`{} {}`))
	require.False(t, ok)
}
