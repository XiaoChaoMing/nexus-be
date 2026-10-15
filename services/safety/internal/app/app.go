// Package app configures and runs safety-svc.
package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/httpmetrics"
	"github.com/minhnn/nexuslive-be/pkg/httpserver"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/pkg/outbox"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/safety/config"
	"github.com/minhnn/nexuslive-be/services/safety/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/safety/internal/controller/restapi"
	v1 "github.com/minhnn/nexuslive-be/services/safety/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/safety/internal/metrics"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo/clients"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo/ratelimit"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase/safety"
)

const (
	_natsConnectTimeout  = 60 * time.Second
	_redisConnectTimeout = 30 * time.Second
	_backfillRetry       = time.Minute
	_purgeTick           = 24 * time.Hour
	_processedRetention  = 30 * 24 * time.Hour
)

// Run creates objects via constructors.
func Run(cfg *config.Config) { //nolint:funlen // linear wiring
	l := logger.New(cfg.Log.Level)

	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(cfg.PG.PoolMax))
	if err != nil {
		l.Fatal("safety - Run - postgres.New: %v", err)
	}
	defer pg.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	natsCtx, natsCancel := context.WithTimeout(ctx, _natsConnectTimeout)
	bus, err := eventbus.Connect(natsCtx, cfg.NATS.URL, cfg.App.Name)

	natsCancel()

	if err != nil {
		l.Fatal("safety - Run - eventbus.Connect: %v", err)
	}

	defer bus.Close()

	redisCtx, redisCancel := context.WithTimeout(ctx, _redisConnectTimeout)
	rdb, err := redis.New(redisCtx, cfg.Redis.Addr)

	redisCancel()

	if err != nil {
		l.Fatal("safety - Run - redis.New: %v", err)
	}

	defer rdb.Close()

	store := persistent.New(pg)
	m := metrics.New()
	uc := safety.New(safety.Repos{
		Reports: store, Actions: store, Audit: persistent.Audit{Repo: store}, Replica: store, Rate: ratelimit.New(rdb),
		Evidence: clients.NewChat(cfg.Internal.ChatURL, cfg.Internal.Secret, cfg.Internal.EvidenceTimeout),
		Snapshot: clients.NewSnapshots(cfg.Internal.IdentityURL, cfg.Internal.ChannelURL, cfg.Internal.StreamURL,
			cfg.Internal.Secret),
	}, l, safety.RateLimit(cfg.Safety.ReportRateLimit), safety.EvidenceTimeout(cfg.Internal.EvidenceTimeout),
		safety.WithObserver(m))

	// safety.* and report_resolved events leave through the outbox.
	go outbox.New(pg.Pool, bus, l).Run(ctx)

	// The copies are patched with a synced_at guard, so the consumer may run while the one-off backfill
	// is still retrying (a source being down must not hold the event stream back).
	stop, err := bus.Consume(ctx, events.Durable, events.Types, events.Handler(uc, uc.AuditLog()), func(e eventbus.Event, herr error) {
		l.Warn("safety - event %s %s: %v", e.Type, e.ID, herr)
	})
	if err != nil {
		l.Fatal("safety - Run - consume: %v", err)
	}
	defer stop()

	go runBackfill(ctx, uc, l)
	go every(ctx, _purgeTick, func() {
		if n, err := store.PurgeProcessed(ctx, _processedRetention); err != nil {
			l.Warn("safety - purge processed events: %v", err)
		} else if n > 0 {
			l.Info("safety - purged %d processed events", n)
		}
	})

	httpServer := httpserver.New(l, httpserver.Port(cfg.HTTP.Port))
	// Before the routes, so every request is counted (GET /metrics included).
	httpServer.App.Use(httpmetrics.New(m.Registerer()))
	restapi.NewRouter(httpServer.App, v1.UseCases{Reports: uc, Queue: uc, Actions: uc, Directory: uc, Audit: uc.AuditLog()},
		jwt.NewJWKSVerifier(cfg.Auth.JWKSURL, cfg.Auth.JWTIssuer), l, cfg.Swagger.Enabled)
	httpServer.App.Get("/metrics", adaptor.HTTPHandler(m.Handler()))

	httpServer.Start()
	l.Info("safety - Run - :%s, report limit %d/h, evidence timeout %s", cfg.HTTP.Port, cfg.Safety.ReportRateLimit,
		cfg.Internal.EvidenceTimeout)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-interrupt:
		l.Info("safety - Run - signal: %s", s.String())
	case err = <-httpServer.Notify():
		l.Error("safety - Run - httpServer.Notify: %v", err)
	}

	cancel()

	if err = httpServer.Shutdown(); err != nil {
		l.Error("safety - Run - httpServer.Shutdown: %v", err)
	}
}

// runBackfill copies the source snapshots once, retrying every minute until it succeeds (TSD §6.10).
func runBackfill(ctx context.Context, uc *safety.UseCase, l logger.Interface) {
	for {
		err := uc.Backfill(ctx)
		if err == nil {
			return
		}

		l.Error("safety - backfill (retry in %s): %v", _backfillRetry, err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(_backfillRetry):
		}
	}
}

// every runs fn on each tick until ctx is canceled.
func every(ctx context.Context, d time.Duration, fn func()) {
	t := time.NewTicker(d)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}
