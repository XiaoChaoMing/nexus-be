// Package metrics exposes channel-svc metrics to Prometheus (GET /metrics).
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns the service registry (no global state).
type Metrics struct {
	reg *prometheus.Registry
}

// New -.
func New() *Metrics {
	m := &Metrics{reg: prometheus.NewRegistry()}

	m.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// Registerer lets transport-level collectors (HTTP request metrics) share the registry.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
