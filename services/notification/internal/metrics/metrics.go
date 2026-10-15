// Package metrics exposes notification-svc counters to Prometheus (GET /metrics).
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics implements usecase.Observer on its own registry (no global state).
type Metrics struct {
	reg        *prometheus.Registry
	created    *prometheus.CounterVec
	suppressed *prometheus.CounterVec
	emails     *prometheus.CounterVec
	sendTime   prometheus.Histogram
	reads      *prometheus.CounterVec
	lag        *prometheus.HistogramVec
}

var _ usecase.Observer = (*Metrics)(nil)

// New -.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		created: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notification_created_total", Help: "Notifications created or grouped, by type.",
		}, []string{"type", "grouped"}),
		suppressed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notification_suppressed_total",
			Help: "Notifications or e-mails not sent, by type and reason (settings, cooldown, blocked, daily_cap, ...).",
		}, []string{"type", "reason"}),
		emails: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notification_emails_total", Help: "E-mail send attempts by template and outcome.",
		}, []string{"template", "status"}),
		sendTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "notification_email_send_seconds", Help: "Time to hand one e-mail to SMTP.",
			Buckets: prometheus.DefBuckets,
		}),
		reads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notification_reads_total", Help: "Read, read-all and delete actions.",
		}, []string{"action"}),
		lag: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "notification_event_lag_seconds", Help: "Source event committed → handled by notification-svc.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 3, 5, 10, 30, 60, 300},
		}, []string{"source"}),
	}

	m.reg.MustRegister(m.created, m.suppressed, m.emails, m.sendTime, m.reads, m.lag,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// Created -.
func (m *Metrics) Created(t entity.Type, grouped bool) {
	m.created.WithLabelValues(string(t), strconv.FormatBool(grouped)).Inc()
}

// Suppressed -.
func (m *Metrics) Suppressed(t entity.Type, reason string) {
	m.suppressed.WithLabelValues(string(t), reason).Inc()
}

// EmailSent -.
func (m *Metrics) EmailSent(template, status string, took time.Duration) {
	m.emails.WithLabelValues(template, status).Inc()

	if status == usecase.EmailSent {
		m.sendTime.Observe(took.Seconds())
	}
}

// Read -.
func (m *Metrics) Read(action string) { m.reads.WithLabelValues(action).Inc() }

// EventLag -.
func (m *Metrics) EventLag(source string, lag time.Duration) {
	m.lag.WithLabelValues(source).Observe(lag.Seconds())
}

// Registerer lets transport-level collectors (HTTP request metrics) share the registry.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
