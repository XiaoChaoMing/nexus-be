package webhook_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/webhook"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/stretchr/testify/require"
)

type payments struct {
	res    usecase.IPNResult
	err    error
	secret string
}

func (p *payments) Notify(_ context.Context, h http.Header, _ []byte) (usecase.IPNResult, error) {
	p.secret = h.Get("X-Secret-Key")

	return p.res, p.err
}

var errDB = errors.New("db down")

func TestWebhook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		res    usecase.IPNResult
		err    error
		body   string
		status int
		reply  string
	}{
		{"applied", usecase.IPNApplied, nil, `{}`, http.StatusOK, `{"success":true}`},
		{"duplicate", usecase.IPNDuplicate, nil, `{}`, http.StatusOK, `{"success":true}`},
		{"mismatch", usecase.IPNMismatch, nil, `{}`, http.StatusOK, `{"success":true}`},
		{"wrong secret", usecase.IPNUnauthorized, entity.ErrUnauthorized, `{}`, http.StatusUnauthorized, `{"success":false}`},
		{"malformed", usecase.IPNMalformed, entity.ErrMalformed, `{`, http.StatusBadRequest, `{"success":false}`},
		{"database down", usecase.IPNError, errDB, `{}`, http.StatusInternalServerError, `{"success":false}`},
		{
			"over 64 KB", usecase.IPNApplied, nil, strings.Repeat(" ", entity.WebhookBodyMax+1), http.StatusBadRequest,
			`{"success":false}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			uc := &payments{res: tt.res, err: tt.err}
			app := fiber.New()
			webhook.Register(app, uc, logger.New("error"))

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, webhook.Path, strings.NewReader(tt.body))
			req.Header.Set("X-Secret-Key", "s")

			resp, err := app.Test(req)
			require.NoError(t, err)

			defer resp.Body.Close()

			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tt.status, resp.StatusCode)
			require.JSONEq(t, tt.reply, string(b))
		})
	}
}
