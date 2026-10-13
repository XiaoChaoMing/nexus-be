// Package app wires and runs media-svc.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	ev "github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/config"
	"github.com/minhnn/nexuslive-be/services/media/internal/authz"
	"github.com/minhnn/nexuslive-be/services/media/internal/events"
	"github.com/minhnn/nexuslive-be/services/media/internal/httpapi"
	"github.com/minhnn/nexuslive-be/services/media/internal/metrics"
	"github.com/minhnn/nexuslive-be/services/media/internal/rtmpserver"
	"github.com/minhnn/nexuslive-be/services/media/internal/session"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
)

const (
	_durable            = "media-svc"
	_natsConnectTimeout = 60 * time.Second
	_bucketRetries      = 30
	_bucketRetryDelay   = 2 * time.Second
	_shutdownTimeout    = 20 * time.Second
	_httpHeaderTimeout  = 5 * time.Second
	// One EnsureBucket attempt may take up to two retry delays before the next try.
	_bucketAttemptTimeout = 2 * _bucketRetryDelay
	_workDirPerm          = 0o750
)

// Run creates objects via constructors.
func Run(cfg *config.Config) { //nolint:funlen,gocognit,gocyclo,cyclop // linear wiring: start-up steps in order
	l := logger.New(cfg.Log.Level)

	standalone := cfg.StreamSvc.URL == ""
	if standalone && cfg.Media.StaticStreamKey == "" {
		l.Fatal("media - Run - set MEDIA_STREAM_SVC_URL, or MEDIA_STATIC_STREAM_KEY for standalone mode")
	}

	if err := os.MkdirAll(cfg.Media.WorkDir, _workDirPerm); err != nil {
		l.Fatal("media - Run - work dir: %v", err)
	}

	store, err := storage.NewMinIO(cfg.S3.Endpoint, cfg.S3.AccessKey, cfg.S3.SecretKey, cfg.S3.Bucket, cfg.S3.UseSSL)
	if err != nil {
		l.Fatal("media - Run - storage: %v", err)
	}

	if cfg.S3.EnsureBucket {
		if err = ensureBucket(store, cfg.S3.RetentionDays, l); err != nil {
			l.Fatal("media - Run - ensure bucket: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	auth, pub, bus := connect(ctx, cfg, l, standalone)
	if bus != nil {
		defer bus.Close()
	}

	m := metrics.New()
	mgr := session.NewManager(session.Config{
		WorkDir:              cfg.Media.WorkDir,
		FFmpegPath:           cfg.Media.FFmpegPath,
		Preset:               cfg.Media.Preset,
		MaxConcurrentStreams: cfg.Media.MaxConcurrentStreams,
		GracePeriod:          cfg.Media.GracePeriod,
		OnReject:             m.PublishRejected,
	}, l, m.InstrumentStore(store), pub)
	m.WatchSessions(mgr)

	recoverStreams(ctx, mgr, l)

	if bus != nil {
		stop, cerr := bus.Consume(ctx, _durable, []string{ev.TypeStreamEnded}, streamEndedHandler(mgr), func(e eventbus.Event, herr error) {
			l.Warn("media - event %s %s: %v", e.Type, e.ID, herr)
		})
		if cerr != nil {
			l.Fatal("media - Run - consume: %v", cerr)
		}
		defer stop()
	}

	go mgr.RunHealth(ctx, cfg.Media.HealthInterval)

	rtmpSrv, err := rtmpserver.New(cfg.RTMP.Addr, l, auth, mgr)
	if err != nil {
		l.Fatal("media - Run - rtmp listen: %v", err)
	}

	scheme := "http"
	if cfg.S3.UseSSL {
		scheme = "https"
	}

	handler, err := httpapi.New(mgr, httpapi.Options{
		DevPlayer:     cfg.HTTP.DevPlayer,
		HLSOrigin:     fmt.Sprintf("%s://%s/%s", scheme, cfg.S3.Endpoint, cfg.S3.Bucket),
		RTMPPublicURL: cfg.RTMP.PublicURL,
		Metrics:       m.Handler(),
	})
	if err != nil {
		l.Fatal("media - Run - http handler: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.HTTP.Port),
		Handler:           handler,
		ReadHeaderTimeout: _httpHeaderTimeout,
	}

	httpErr := make(chan error, 1)

	go func() {
		if serveErr := httpSrv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			httpErr <- serveErr
		}
	}()

	rtmpSrv.Start()
	l.Info("media - Run - RTMP on %s, HTTP on :%s, max %d streams", rtmpSrv.Addr(), cfg.HTTP.Port, cfg.Media.MaxConcurrentStreams)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-interrupt:
		l.Info("media - Run - signal: %s", s.String())
	case err = <-rtmpSrv.Notify():
		l.Error("media - Run - rtmp server: %v", err)
	case err = <-httpErr:
		l.Error("media - Run - http server: %v", err)
	}

	// Stop accepting publishes, then kick and finalize every stream (ENDLIST + last upload).
	if err = rtmpSrv.Close(); err != nil {
		l.Warn("media - Run - rtmp close: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), _shutdownTimeout)
	defer shutdownCancel()

	if err = mgr.Shutdown(shutdownCtx); err != nil {
		l.Error("media - Run - sessions shutdown: %v", err)
	}

	if err = httpSrv.Shutdown(shutdownCtx); err != nil {
		l.Error("media - Run - http shutdown: %v", err)
	}
}

// connect picks the stream-key check and the event sink: stream-svc + NATS, or the standalone
// static key with logged events. bus is nil in standalone mode.
func connect(ctx context.Context, cfg *config.Config, l logger.Interface, standalone bool) (authz.Authorizer, events.Publisher, *eventbus.Bus) {
	if standalone {
		l.Warn("media - Run - standalone mode: static stream key, events only logged")

		return authz.NewStatic(cfg.Media.StaticStreamKey), events.NewLog(l), nil
	}

	natsCtx, natsCancel := context.WithTimeout(ctx, _natsConnectTimeout)
	defer natsCancel()

	bus, err := eventbus.Connect(natsCtx, cfg.StreamSvc.NATSURL, cfg.App.Name)
	if err != nil {
		l.Fatal("media - Run - eventbus.Connect: %v", err)
	}

	return authz.NewHTTP(cfg.StreamSvc.URL, cfg.StreamSvc.InternalSecret), events.NewBus(bus, l), bus
}

// recoverStreams turns streams left behind by a crash into VODs before OBS can reconnect (they
// would otherwise stay INTERRUPTED in stream-svc with nobody to finish them).
func recoverStreams(ctx context.Context, mgr *session.Manager, l logger.Interface) {
	rec, err := mgr.Recover(ctx)
	if err != nil {
		l.Error("media - Run - recover: %v", err)

		return
	}

	if rec != (session.RecoverResult{}) {
		l.Info("media - Run - recovered %d stream(s), discarded %d, kept %d for retry", rec.Recovered, rec.Discarded, rec.Kept)
	}
}

// streamEndedHandler kicks the publisher and finalizes the VOD when stream-svc ends a stream
// (creator pressed End, or the grace period ran out). Unknown streams are ignored.
func streamEndedHandler(mgr *session.Manager) eventbus.Handler {
	return func(_ context.Context, e eventbus.Event) error {
		var p ev.StreamEnded
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("%w: %w", eventbus.ErrSkip, err)
		}

		mgr.EndStream(p.StreamID)

		return nil
	}
}

// ensureBucket retries because MinIO may still be starting under compose.
func ensureBucket(store *storage.MinIO, retentionDays int, l logger.Interface) error {
	var err error

	for i := range _bucketRetries {
		ctx, cancel := context.WithTimeout(context.Background(), _bucketAttemptTimeout)
		err = store.EnsureBucket(ctx, retentionDays)

		cancel()

		if err == nil {
			return nil
		}

		l.Warn("media - storage not ready (attempt %d/%d): %v", i+1, _bucketRetries, err)
		time.Sleep(_bucketRetryDelay)
	}

	return err
}
