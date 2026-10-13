// Package metrics exposes stream-svc counters to Prometheus (GET /metrics).
package metrics

import (
	"net/http"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics implements usecase.Observer on its own registry (no global state).
type Metrics struct {
	reg         *prometheus.Registry
	liveStreams prometheus.Gauge
	transitions *prometheus.CounterVec
	authorize   *prometheus.CounterVec
}

var _ usecase.Observer = (*Metrics)(nil)

// New -.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		liveStreams: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "stream_live_streams",
			Help: "Streams on air (LIVE or INTERRUPTED).",
		}),
		transitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stream_transitions_total",
			Help: "Stream state transitions by target state.",
		}, []string{"to"}),
		authorize: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stream_authorize_total",
			Help: "media-svc authorize calls by result.",
		}, []string{"result"}),
	}

	m.reg.MustRegister(m.liveStreams, m.transitions, m.authorize,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// Authorize -.
func (m *Metrics) Authorize(result string) { m.authorize.WithLabelValues(result).Inc() }

// Transition -.
func (m *Metrics) Transition(to entity.Status) { m.transitions.WithLabelValues(string(to)).Inc() }

// OnAir -.
func (m *Metrics) OnAir(n int) { m.liveStreams.Set(float64(n)) }

// Registerer lets transport-level collectors (HTTP request metrics) share the registry.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
