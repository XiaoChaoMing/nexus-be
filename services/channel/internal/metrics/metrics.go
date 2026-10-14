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
	reg         *prometheus.Registry
	jobFailures *prometheus.CounterVec
}

// New -.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		jobFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "channel_job_failures_total",
			Help: "Failed runs of background jobs (announce_upcoming, due_reminders, search_viewers).",
		}, []string{"job"}),
	}

	m.reg.MustRegister(m.jobFailures,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// JobFailed counts one failed run of job.
func (m *Metrics) JobFailed(job string) { m.jobFailures.WithLabelValues(job).Inc() }

// Registerer lets transport-level collectors (HTTP request metrics) share the registry.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
