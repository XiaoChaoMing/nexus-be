// Package app configures and runs stream-svc.
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
	"github.com/minhnn/nexuslive-be/services/stream/config"
	"github.com/minhnn/nexuslive-be/services/stream/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/controller/internalapi"
	"github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi"
	v1 "github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/stream/internal/metrics"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/streams"
)

const _natsConnectTimeout = 60 * time.Second

// Run creates objects via constructors.
func Run(cfg *config.Config) { //nolint:funlen,gocyclo,cyclop // linear wiring: one branch per fatal start-up error
	l := logger.New(cfg.Log.Level)

	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(cfg.PG.PoolMax))
	if err != nil {
		l.Fatal("stream - Run - postgres.New: %v", err)
	}
	defer pg.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	natsCtx, natsCancel := context.WithTimeout(ctx, _natsConnectTimeout)
	bus, err := eventbus.Connect(natsCtx, cfg.NATS.URL, cfg.App.Name)

	natsCancel()

	if err != nil {
		l.Fatal("stream - Run - eventbus.Connect: %v", err)
	}

	defer bus.Close()

	store := persistent.New(pg)
	m := metrics.New()
	uc := streams.New(store, store, store, store, cfg.Stream.GracePeriod, cfg.Stream.VODRetention, l,
		streams.EndCooldown(cfg.Stream.EndCooldown), streams.WithObserver(m))

	// Outgoing events (stream.started / stream.ended) leave through the outbox.
	go outbox.New(pg.Pool, bus, l).Run(ctx)

	stopConsumer, err := bus.Consume(ctx, events.Durable, events.Types, events.Handler(uc, uc), func(e eventbus.Event, herr error) {
		l.Warn("stream - event %s %s: %v", e.Type, e.ID, herr)
	})
	if err != nil {
		l.Fatal("stream - Run - consume: %v", err)
	}
	defer stopConsumer()

	go every(ctx, cfg.Stream.ExpiryTick, func() {
		if _, err := uc.ExpireInterrupted(ctx); err != nil {
			l.Warn("stream - expiry: %v", err)
		}
	})
	go every(ctx, cfg.Stream.VODExpiryTick, func() {
		if _, err := uc.ExpireVODs(ctx); err != nil {
			l.Warn("stream - vod expiry: %v", err)
		}
	})

	public := httpserver.New(l, httpserver.Port(cfg.HTTP.Port))
	// Before the routes, so every request is counted (GET /metrics included).
	public.App.Use(httpmetrics.New(m.Registerer()))
	restapi.NewRouter(public.App, uc, uc, jwt.NewJWKSVerifier(cfg.Auth.JWKSURL, cfg.Auth.JWTIssuer), l, v1.Options{
		RTMPPublicURL: cfg.Stream.RTMPPublicURL,
		HLSBase:       cfg.Stream.HLSBase,
	}, cfg.Swagger.Enabled)
	public.App.Get("/metrics", adaptor.HTTPHandler(m.Handler()))

	internal := httpserver.New(l, httpserver.Port(cfg.HTTP.InternalPort))
	internalapi.NewRouter(internal.App, uc, cfg.HTTP.InternalSecret, l)

	public.Start()
	internal.Start()

	l.Info("stream - Run - public :%s, internal :%s, grace %s", cfg.HTTP.Port, cfg.HTTP.InternalPort, cfg.Stream.GracePeriod)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-interrupt:
		l.Info("stream - Run - signal: %s", s.String())
	case err = <-public.Notify():
		l.Error("stream - Run - public server: %v", err)
	case err = <-internal.Notify():
		l.Error("stream - Run - internal server: %v", err)
	}

	cancel()

	if err = public.Shutdown(); err != nil {
		l.Error("stream - Run - public shutdown: %v", err)
	}

	if err = internal.Shutdown(); err != nil {
		l.Error("stream - Run - internal shutdown: %v", err)
	}
}

// every runs fn on each tick until ctx is canceled (background expiry jobs).
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
