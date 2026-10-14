// Package metrics exposes chat-svc counters to Prometheus (GET /metrics).
package metrics

import (
	"net/http"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics implements usecase.Observer and the gateway's ws.Metrics on its own registry.
type Metrics struct {
	reg           *prometheus.Registry
	connections   prometheus.Gauge
	subscriptions prometheus.Gauge
	sent          prometheus.Counter
	delivered     prometheus.Counter
	latency       prometheus.Histogram
	rejects       *prometheus.CounterVec
	resyncs       prometheus.Counter
}

var _ usecase.Observer = (*Metrics)(nil)

// New -.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		connections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chat_ws_connections", Help: "Open WebSocket connections on this instance.",
		}),
		subscriptions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chat_ws_subscriptions", Help: "Topic subscriptions on this instance.",
		}),
		sent: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "chat_messages_sent_total", Help: "Chat messages accepted and stored by this instance.",
		}),
		delivered: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "chat_events_delivered_total", Help: "Realtime event frames queued to sockets (all topics).",
		}),
		latency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "chat_fanout_latency_seconds",
			Help:    "Time from an event's creation to its delivery by this instance's topic actor.",
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}),
		rejects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "chat_send_rejects_total", Help: "Refused sends by error code (RATE_LIMITED, CHAT_SLOW_MODE, ...).",
		}, []string{"code"}),
		resyncs: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "chat_ws_resyncs_total", Help: "resync frames sent (resume impossible or hole in the sequence).",
		}),
	}

	m.reg.MustRegister(m.connections, m.subscriptions, m.sent, m.delivered, m.latency, m.rejects, m.resyncs,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// MessageSent -.
func (m *Metrics) MessageSent() { m.sent.Inc() }

// Rejected -.
func (m *Metrics) Rejected(code string) { m.rejects.WithLabelValues(code).Inc() }

// ConnOpened -.
func (m *Metrics) ConnOpened() { m.connections.Inc() }

// ConnClosed -.
func (m *Metrics) ConnClosed() { m.connections.Dec() }

// Subscriptions -.
func (m *Metrics) Subscriptions(delta int) { m.subscriptions.Add(float64(delta)) }

// Delivered -.
func (m *Metrics) Delivered(n int) { m.delivered.Add(float64(n)) }

// FanoutLatency -.
func (m *Metrics) FanoutLatency(d time.Duration) { m.latency.Observe(max(d, 0).Seconds()) }

// Resync -.
func (m *Metrics) Resync() { m.resyncs.Inc() }

// Registerer lets transport-level collectors (HTTP request metrics) share the registry.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
