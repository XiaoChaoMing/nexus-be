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
	vodDeleted   prometheus.Counter

	uploadJobs      *prometheus.CounterVec
	uploadTranscode prometheus.Histogram
	uploadSpeed     prometheus.Gauge
	uploadQueueWait prometheus.Histogram
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
		vodDeleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "media_vod_objects_deleted_total",
			Help: "Objects removed for VODs deleted by their creator (vod.deleted.v1).",
		}),
	}

	m.addUploadJobs()

	m.reg.MustRegister(m.uploadSecs, m.uploadErrors, m.rejected, m.vodDeleted,
		m.uploadJobs, m.uploadTranscode, m.uploadSpeed, m.uploadQueueWait,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return m
}

// addUploadJobs creates the metrics of uploaded-video transcoding (internal/transcode).
func (m *Metrics) addUploadJobs() {
	m.uploadJobs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "media_upload_jobs_total",
		Help: "Uploaded-video transcoding jobs by result (succeeded, failed, retryable, skipped, requeued).",
	}, []string{"result"})
	m.uploadTranscode = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "media_upload_transcode_seconds",
		Help:    "Wall time of ffmpeg for one uploaded video.",
		Buckets: prometheus.ExponentialBuckets(15, 2, 10), //nolint:mnd // 15 s … ~2 h
	})
	m.uploadSpeed = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "media_upload_transcode_speed",
		Help: "ffmpeg speed of the running upload job (1.0 = real time, 0 = idle); drops while live streams run.",
	})
	m.uploadQueueWait = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "media_upload_queue_wait_seconds",
		Help:    "Time from upload.completed.v1 to the start of its transcoding job.",
		Buckets: prometheus.ExponentialBuckets(1, 3, 10), //nolint:mnd // 1 s … ~5.5 h
	})
}

// UploadJob counts one finished upload job.
func (m *Metrics) UploadJob(result string) { m.uploadJobs.WithLabelValues(result).Inc() }

// UploadTranscoded observes ffmpeg's wall time for one upload job.
func (m *Metrics) UploadTranscoded(d time.Duration) { m.uploadTranscode.Observe(d.Seconds()) }

// UploadSpeed sets the running job's ffmpeg speed.
func (m *Metrics) UploadSpeed(speed float64) { m.uploadSpeed.Set(speed) }

// UploadQueueWait observes how long a job waited for a slot.
func (m *Metrics) UploadQueueWait(d time.Duration) { m.uploadQueueWait.Observe(d.Seconds()) }

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

// VODObjectsDeleted -.
func (m *Metrics) VODObjectsDeleted(n int) { m.vodDeleted.Add(float64(n)) }

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
