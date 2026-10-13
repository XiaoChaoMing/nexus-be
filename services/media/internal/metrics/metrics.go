// Package metrics exposes media-svc counters to Prometheus (GET /metrics).
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/minhnn/nexuslive-be/services/media/internal/session"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Sessions is the read side of session.Manager the collectors need.
type Sessions interface {
	Active() int
	List() []session.Info
}

// Metrics owns its registry (no global state).
type Metrics struct {
	reg          *prometheus.Registry
	uploadSecs   prometheus.Histogram
	uploadErrors prometheus.Counter
	rejected     *prometheus.CounterVec
}

// New -.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		uploadSecs: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "media_upload_seconds",
			Help:    "Duration of one object upload to storage (segments, playlists, thumbnails).",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
		}),
		uploadErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "media_upload_errors_total",
			Help: "Failed object uploads (each attempt).",
		}),
		rejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "media_publish_rejected_total",
			Help: "Refused RTMP publishes by reason.",
		}, []string{"reason"}),
	}

	m.reg.MustRegister(m.uploadSecs, m.uploadErrors, m.rejected,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// WatchSessions adds the per-stream collectors; the manager is built after the store is instrumented.
func (m *Metrics) WatchSessions(sessions Sessions) {
	m.reg.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "media_active_streams",
			Help: "Streams currently transcoding (live or starting).",
		}, func() float64 { return float64(sessions.Active()) }),
		&sessionCollector{
			sessions: sessions,
			speed: prometheus.NewDesc("media_ffmpeg_speed",
				"ffmpeg processing speed per live stream (1.0 = real time).", []string{"stream"}, nil),
			dropped: prometheus.NewDesc("media_dropped_frames_total",
				"Frames dropped by ffmpeg for the stream's current publish (restarts at 0 on reconnect).", []string{"stream"}, nil),
		})
}

// PublishRejected -.
func (m *Metrics) PublishRejected(reason string) { m.rejected.WithLabelValues(reason).Inc() }

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// InstrumentStore times every upload and counts the failed ones.
func (m *Metrics) InstrumentStore(s storage.ObjectStore) storage.ObjectStore {
	return &instrumentedStore{next: s, m: m}
}

type instrumentedStore struct {
	next storage.ObjectStore
	m    *Metrics
}

func (s *instrumentedStore) Put(ctx context.Context, key string, body []byte, contentType, cacheControl string) error {
	start := time.Now()
	err := s.next.Put(ctx, key, body, contentType, cacheControl)

	s.m.uploadSecs.Observe(time.Since(start).Seconds())

	if err != nil {
		s.m.uploadErrors.Inc()
	}

	return err
}

// sessionCollector reads per-stream ffmpeg progress at scrape time.
type sessionCollector struct {
	sessions       Sessions
	speed, dropped *prometheus.Desc
}

func (c *sessionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.speed

	ch <- c.dropped
}

func (c *sessionCollector) Collect(ch chan<- prometheus.Metric) {
	for _, in := range c.sessions.List() {
		if in.State != session.StateLive {
			continue
		}

		ch <- prometheus.MustNewConstMetric(c.speed, prometheus.GaugeValue, in.Progress.Speed, in.StreamID)

		ch <- prometheus.MustNewConstMetric(c.dropped, prometheus.CounterValue, float64(in.Progress.DropFrames), in.StreamID)
	}
}
