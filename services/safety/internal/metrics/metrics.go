// Package metrics exposes safety-svc counters to Prometheus (GET /metrics).
package metrics

import (
	"net/http"

	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics implements usecase.Observer on its own registry (no global state).
type Metrics struct {
	reg     *prometheus.Registry
	reports *prometheus.CounterVec
}

var _ usecase.Observer = (*Metrics)(nil)

// New -.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		reports: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "safety_reports_total",
			Help: "Reports filed by target type and reason (duplicates not counted).",
		}, []string{"target_type", "reason"}),
	}

	m.reg.MustRegister(m.reports,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// ReportFiled -.
func (m *Metrics) ReportFiled(targetType, reason string) {
	m.reports.WithLabelValues(targetType, reason).Inc()
}

// Registerer lets transport-level collectors (HTTP request metrics) share the registry.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
