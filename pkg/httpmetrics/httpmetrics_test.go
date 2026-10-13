package httpmetrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/httpmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestMiddlewareLabelsByRoutePattern(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	app := fiber.New()
	app.Use(httpmetrics.New(reg))
	app.Get("/v1/streams/:id", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })
	app.Get("/v1/fail", func(*fiber.Ctx) error { return fiber.ErrBadRequest })

	for _, path := range []string{"/v1/streams/a", "/v1/streams/b", "/v1/fail", "/nope/123"} {
		resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	families, err := reg.Gather()
	require.NoError(t, err)

	counts := map[string]float64{}
	histograms := 0

	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}

			switch mf.GetName() {
			case "http_requests_total":
				counts[labels["route"]+" "+labels["status"]] = m.GetCounter().GetValue()
			case "http_request_duration_seconds":
				histograms++
			}
		}
	}

	require.Equal(t, map[string]float64{
		"/v1/streams/:id 200": 2,
		"/v1/fail 400":        1,
		"unmatched 404":       1,
	}, counts)
	require.Equal(t, 3, histograms)
}
