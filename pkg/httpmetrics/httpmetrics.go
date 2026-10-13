// Package httpmetrics records request counts and latency of a Fiber app for Prometheus.
package httpmetrics

import (
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
)

// UnmatchedRoute labels requests that matched no route, so raw paths never become label values.
const UnmatchedRoute = "unmatched"

// New registers http_requests_total{method,route,status} and
// http_request_duration_seconds{method,route} on reg and returns the middleware.
// route is the matched route pattern (e.g. /v1/streams/:id), never the raw path.
func New(reg prometheus.Registerer) fiber.Handler {
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests by method, route pattern and status code.",
	}, []string{"method", "route", "status"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by method and route pattern.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.4, 0.8, 1.6, 3.2},
	}, []string{"method", "route"})

	reg.MustRegister(requests, duration)

	return func(ctx *fiber.Ctx) error {
		start := time.Now()
		err := ctx.Next()

		status := ctx.Response().StatusCode()

		// The error handler writes the status after this middleware returns.
		if err != nil {
			status = fiber.StatusInternalServerError

			var fe *fiber.Error
			if errors.As(err, &fe) {
				status = fe.Code
			}
		}

		method := ctx.Method()
		route := routeOf(ctx)

		requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
		duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())

		return err
	}
}

func routeOf(ctx *fiber.Ctx) string {
	r := ctx.Route()
	// Fiber reports "/" for the catch-all of an app without a matching route.
	if r == nil || r.Path == "" || (r.Path == "/" && ctx.Path() != "/") {
		return UnmatchedRoute
	}

	return r.Path
}
