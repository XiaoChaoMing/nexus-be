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

// Fiber hands out the method as a view of a pooled buffer; a label kept from it would later read as
// another request's method and /metrics would fail with duplicate series.
func TestMiddlewareKeepsLabelsOfEarlierRequests(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	app := fiber.New()
	app.Use(httpmetrics.New(reg))
	app.Put("/v1/x", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })
	app.Get("/v1/x", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })

	for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodGet, http.MethodPut} {
		resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), method, "/v1/x", http.NoBody))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	families, err := reg.Gather()
	require.NoError(t, err, "no duplicate series")

	methods := map[string]float64{}

	for _, mf := range families {
		if mf.GetName() != "http_requests_total" {
			continue
		}

		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "method" {
					methods[lp.GetValue()] = m.GetCounter().GetValue()
				}
			}
		}
	}

	require.Equal(t, map[string]float64{http.MethodGet: 2, http.MethodPut: 2}, methods)
}
