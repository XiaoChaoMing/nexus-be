// Package app configures and runs channel-svc.
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
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/pkg/outbox"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/channel/config"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi"
	"github.com/minhnn/nexuslive-be/services/channel/internal/metrics"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/channels"
)

const _natsConnectTimeout = 60 * time.Second

// Run creates objects via constructors.
func Run(cfg *config.Config) {
	l := logger.New(cfg.Log.Level)

	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(cfg.PG.PoolMax))
	if err != nil {
		l.Fatal("channel - Run - postgres.New: %v", err)
	}
	defer pg.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	natsCtx, natsCancel := context.WithTimeout(ctx, _natsConnectTimeout)
	bus, err := eventbus.Connect(natsCtx, cfg.NATS.URL, cfg.App.Name)

	natsCancel()

	if err != nil {
		l.Fatal("channel - Run - eventbus.Connect: %v", err)
	}

	defer bus.Close()

	uc := channels.New(persistent.New(pg), l)
	m := metrics.New()

	// Outgoing events (channel.created) leave through the outbox.
	go outbox.New(pg.Pool, bus, l).Run(ctx)

	stopConsumer, err := bus.Consume(ctx, events.Durable, events.Types, events.Handler(uc), func(e eventbus.Event, herr error) {
		l.Warn("channel - event %s %s: %v", e.Type, e.ID, herr)
	})
	if err != nil {
		l.Fatal("channel - Run - consume: %v", err)
	}
	defer stopConsumer()

	public := httpserver.New(l, httpserver.Port(cfg.HTTP.Port))
	// Before the routes, so every request is counted (GET /metrics included).
	public.App.Use(httpmetrics.New(m.Registerer()))
	restapi.NewRouter(public.App, uc, l, cfg.Swagger.Enabled)
	public.App.Get("/metrics", adaptor.HTTPHandler(m.Handler()))

	public.Start()

	l.Info("channel - Run - public :%s", cfg.HTTP.Port)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-interrupt:
		l.Info("channel - Run - signal: %s", s.String())
	case err = <-public.Notify():
		l.Error("channel - Run - public server: %v", err)
	}

	cancel()

	if err = public.Shutdown(); err != nil {
		l.Error("channel - Run - public shutdown: %v", err)
	}
}
