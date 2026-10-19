// Package metrics exposes billing-svc metrics to Prometheus (GET /metrics) on its own registry.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics implements usecase.Observer and the provider adapter's observer.
type Metrics struct {
	reg            *prometheus.Registry
	ipn            *prometheus.CounterVec
	ipnDuration    prometheus.Histogram
	orders         *prometheus.CounterVec
	providerCalls  *prometheus.CounterVec
	providerTiming *prometheus.HistogramVec
	reconcileRuns  *prometheus.CounterVec
	mismatches     *prometheus.CounterVec
	needsReview    prometheus.Gauge
	activeSubs     *prometheus.GaugeVec
	voids          prometheus.Counter
	payouts        *prometheus.CounterVec
	idempotency    *prometheus.CounterVec
}

var _ usecase.Observer = (*Metrics)(nil)

// New -.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		ipn: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_ipn_total", Help: "Provider notifications by type and result.",
		}, []string{"type", "result"}),
		ipnDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "billing_ipn_duration_seconds", Help: "Time to handle a provider notification.",
			Buckets: prometheus.DefBuckets,
		}),
		orders: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_checkout_orders_total", Help: "Checkout orders reaching a status.",
		}, []string{"status", "method", "reason"}),
		providerCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_provider_requests_total", Help: "Payment provider API calls by operation and result.",
		}, []string{"op", "result"}),
		providerTiming: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "billing_provider_duration_seconds", Help: "Payment provider API latency.", Buckets: prometheus.DefBuckets,
		}, []string{"op"}),
		reconcileRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_reconcile_runs_total", Help: "Job runs by job and result.",
		}, []string{"job", "result"}),
		mismatches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_reconcile_mismatches_total", Help: "Daily reconciliation differences by kind.",
		}, []string{"kind"}),
		needsReview: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "billing_orders_needs_review", Help: "Paid orders waiting for an admin review.",
		}),
		activeSubs: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "billing_subscriptions_active", Help: "Live subscriptions by tier.",
		}, []string{"tier"}),
		voids: prometheus.NewCounter(prometheus.CounterOpts{Name: "billing_voids_total", Help: "Voided payments."}),
		payouts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_payouts_total", Help: "Payout requests reaching a status.",
		}, []string{"status"}),
		idempotency: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "billing_idempotency_total", Help: "Idempotency-Key lookups by result.",
		}, []string{"result"}),
	}

	m.reg.MustRegister(m.ipn, m.ipnDuration, m.orders, m.providerCalls, m.providerTiming, m.reconcileRuns, m.mismatches,
		m.needsReview, m.activeSubs, m.voids, m.payouts, m.idempotency,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// IPN -.
func (m *Metrics) IPN(typ string, result usecase.IPNResult, d time.Duration) {
	if typ == "" {
		typ = "unknown"
	}

	m.ipn.WithLabelValues(typ, string(result)).Inc()
	m.ipnDuration.Observe(d.Seconds())
}

// OrderStatus -.
func (m *Metrics) OrderStatus(status entity.OrderStatus, method, reason string) {
	m.orders.WithLabelValues(string(status), method, reason).Inc()
}

// ProviderCall -.
func (m *Metrics) ProviderCall(op, result string, d time.Duration) {
	m.providerCalls.WithLabelValues(op, result).Inc()
	m.providerTiming.WithLabelValues(op).Observe(d.Seconds())
}

// ReconcileRun -.
func (m *Metrics) ReconcileRun(job, result string) {
	m.reconcileRuns.WithLabelValues(job, result).Inc()
}

// ReconcileMismatch -.
func (m *Metrics) ReconcileMismatch(kind string) { m.mismatches.WithLabelValues(kind).Inc() }

// NeedsReview -.
func (m *Metrics) NeedsReview(n int) { m.needsReview.Set(float64(n)) }

// ActiveSubscriptions -.
func (m *Metrics) ActiveSubscriptions(byTier map[int]int) {
	for level := 1; level <= entity.TierLevels; level++ {
		m.activeSubs.WithLabelValues(strconv.Itoa(level)).Set(float64(byTier[level]))
	}
}

// Voided -.
func (m *Metrics) Voided() { m.voids.Inc() }

// PayoutStatus -.
func (m *Metrics) PayoutStatus(status entity.PayoutStatus) {
	m.payouts.WithLabelValues(string(status)).Inc()
}

// Idempotency -.
func (m *Metrics) Idempotency(result string) { m.idempotency.WithLabelValues(result).Inc() }

// Registerer lets transport-level collectors (HTTP request metrics) share the registry.
func (m *Metrics) Registerer() prometheus.Registerer { return m.reg }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler { return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}) }
