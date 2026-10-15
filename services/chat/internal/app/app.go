// Package app configures and runs chat-svc.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/minhnn/nexuslive-be/services/chat/config"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/internalapi"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi"
	v1 "github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/ws"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/metrics"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/broker"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/realtime"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/snapshot"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/chat"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/fanout"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/gateway"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/moderation"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/rooms"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/viewers"
)

const (
	_connectTimeout  = 60 * time.Second
	_shutdownTimeout = 8 * time.Second
	_instanceIDBytes = 4
	// _backfillRetry is how often a failed backfill of the channel copy is tried again.
	_backfillRetry = time.Minute
)

type useCases struct {
	chat       *chat.UseCase
	viewers    *viewers.UseCase
	gateway    *gateway.UseCase
	rooms      *rooms.UseCase
	moderation *moderation.UseCase
}

// Run creates objects via constructors.
func Run(cfg *config.Config) { //nolint:funlen,gocyclo,cyclop // linear wiring: one branch per fatal start-up error
	l := logger.New(cfg.Log.Level)

	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(cfg.PG.PoolMax))
	if err != nil {
		l.Fatal("chat - Run - postgres.New: %v", err)
	}
	defer pg.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connectCtx, connectCancel := context.WithTimeout(ctx, _connectTimeout)

	bus, err := eventbus.Connect(connectCtx, cfg.NATS.URL, cfg.App.Name)
	if err != nil {
		connectCancel()
		l.Fatal("chat - Run - eventbus.Connect: %v", err)
	}

	defer bus.Close()

	rdb, err := redis.New(connectCtx, cfg.Redis.Addr)

	connectCancel()

	if err != nil {
		l.Fatal("chat - Run - redis.New: %v", err)
	}

	defer rdb.Close()

	m := metrics.New()
	br := broker.New(bus.NC)
	uc := wire(cfg, pg, rdb, br, m, l)

	go outbox.New(pg.Pool, bus, l).Run(ctx)
	go backfill(ctx, uc.rooms, l)

	stopConsumer, err := bus.Consume(ctx, events.Durable, events.Types, events.Handler(uc.rooms, uc.rooms, uc.rooms), func(e eventbus.Event, herr error) {
		l.Warn("chat - event %s %s: %v", e.Type, e.ID, herr)
	})
	if err != nil {
		l.Fatal("chat - Run - consume: %v", err)
	}
	defer stopConsumer()

	if err = uc.rooms.MaintainPartitions(ctx); err != nil {
		l.Warn("chat - partitions: %v", err)
	}

	go every(ctx, cfg.Chat.PartitionTick, func() {
		if perr := uc.rooms.MaintainPartitions(ctx); perr != nil {
			l.Warn("chat - partitions: %v", perr)
		}
	})
	go every(ctx, cfg.Chat.ViewerInterval, func() {
		if verr := uc.viewers.Tick(ctx); verr != nil {
			l.Warn("chat - viewers: %v", verr)
		}
	})

	server := httpserver.New(l, httpserver.Port(cfg.HTTP.Port))
	// Before the routes, so every request is counted (GET /metrics included).
	server.App.Use(httpmetrics.New(m.Registerer()))
	restapi.NewRouter(server.App, v1.UseCases{Chat: uc.chat, Moderation: uc.moderation, Roles: uc.moderation},
		jwt.NewJWKSVerifier(cfg.Auth.JWKSURL, cfg.Auth.JWTIssuer), l, cfg.Swagger.Enabled)
	server.App.Get("/metrics", adaptor.HTTPHandler(m.Handler()))

	hub := ws.NewHub(uc.gateway, uc.chat, ws.Options{
		AuthTimeout:       cfg.WS.AuthTimeout,
		Heartbeat:         cfg.WS.Heartbeat,
		IdleTimeout:       cfg.WS.IdleTimeout,
		MaxSubscriptions:  cfg.WS.MaxSubscriptions,
		SendQueue:         cfg.WS.SendQueue,
		ResumeMaxGap:      cfg.WS.ResumeBuffer,
		AllowedOrigins:    cfg.WS.AllowedOrigins,
		SamplingThreshold: cfg.Chat.SamplingThreshold,
		SamplingCap:       cfg.Chat.SamplingCap,
	}, m, l)
	hub.Register(server.App)

	go hub.RunSidebarBatch(ctx, cfg.Chat.SidebarBatchInterval)

	// Every instance closes its own sockets of a locked account (the event reaches one instance only).
	stopControl, err := br.SubscribeControl(func(c entity.Control) {
		if n := hub.CloseUser(c.UserID, c.Code, c.Reason); n > 0 {
			l.Info("chat - closed %d connection(s) of %s (%d)", n, c.UserID, c.Code)
		}
	})
	if err != nil {
		l.Fatal("chat - Run - control: %v", err)
	}
	defer stopControl()

	internal := httpserver.New(l, httpserver.Port(cfg.HTTP.InternalPort))
	internalapi.NewRouter(internal.App, uc.moderation, cfg.HTTP.InternalSecret, l)

	server.Start()
	internal.Start()

	l.Info("chat - Run - :%s (WebSocket /v1/ws + REST), internal :%s, disabled=%t", cfg.HTTP.Port, cfg.HTTP.InternalPort, cfg.Chat.Disabled)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-interrupt:
		l.Info("chat - Run - signal: %s", s.String())
	case err = <-server.Notify():
		l.Error("chat - Run - server: %v", err)
	case err = <-internal.Notify():
		l.Error("chat - Run - internal server: %v", err)
	}

	// Sockets first (1012: clients reconnect to the other instance), then the HTTP server.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), _shutdownTimeout)
	hub.Shutdown(shutdownCtx)
	shutdownCancel()
	cancel()

	if err = server.Shutdown(); err != nil {
		l.Error("chat - Run - shutdown: %v", err)
	}

	if err = internal.Shutdown(); err != nil {
		l.Error("chat - Run - internal shutdown: %v", err)
	}
}

// backfill copies channel-svc's channels once, retrying until it succeeds or the service stops.
func backfill(ctx context.Context, r *rooms.UseCase, l logger.Interface) {
	for {
		err := r.BackfillChannels(ctx)
		if err == nil {
			return
		}

		l.Warn("chat - channel backfill (retry in %s): %v", _backfillRetry, err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(_backfillRetry):
		}
	}
}

func wire(cfg *config.Config, pg *postgres.Postgres, rdb *redis.Client, br *broker.Broker, m *metrics.Metrics, l logger.Interface) useCases {
	store := persistent.New(pg)
	rt := realtime.New(rdb, realtime.Options{
		ResumeBuffer: cfg.WS.ResumeBuffer, ResumeTTL: cfg.WS.ResumeTTL, DuplicateWindow: cfg.Chat.DuplicateWindow,
	})
	pub := fanout.New(rt, br, l)

	modUC := moderation.New(moderation.Repos{
		Rooms: store, Mod: store, Terms: store, Roles: store, Users: store, Audience: rt, Live: rt,
	}, pub, moderation.Config{TimeoutMax: cfg.Chat.TimeoutMax}, l)

	chatUC := chat.New(chat.Repos{
		Rooms: store, Messages: store, Settings: store, Roles: store, Emotes: store, Users: store, Guard: rt, Audience: rt,
	}, pub, chat.Config{
		RateLimit:           cfg.Chat.RateLimit,
		RateLimitPrivileged: cfg.Chat.RateLimitPrivileged,
		RateLimitIP:         cfg.Chat.RateLimitIP,
		RateWindow:          cfg.Chat.RateWindow,
		MaxLines:            cfg.Chat.MaxLines,
		HistoryDefault:      cfg.Chat.HistoryOnJoin,
		Disabled:            cfg.Chat.Disabled,
		RequireVerified:     cfg.Chat.RequireVerified,
	}, l, chat.WithObserver(m), chat.WithAutoMod(modUC), chat.WithPins(modUC))

	viewersUC := viewers.New(rt, pub, instanceID(), cfg.Chat.ViewerInterval, l)

	return useCases{
		chat:    chatUC,
		viewers: viewersUC,
		gateway: gateway.New(gateway.Deps{
			Verifier: jwt.NewJWKSVerifier(cfg.Auth.JWKSURL, cfg.Auth.JWTIssuer),
			Chat:     chatUC, Viewers: viewersUC, Seq: rt, Presence: rt, Broker: br, Audience: rt, Live: rt, Locks: rt,
		}, cfg.WS.MaxConnPerUser, l),
		rooms: rooms.New(rooms.Repos{
			Rooms: store, Messages: store, Users: store, Roles: store, Audience: rt, Presence: rt, Live: rt,
			Locks: rt, Broker: br, Snapshot: snapshot.New(cfg.HTTP.ChannelInternalURL, cfg.HTTP.InternalSecret),
		}, pub, cfg.Chat.Retention, l),
		moderation: modUC,
	}
}

// instanceID names this process in the viewer-count lease.
func instanceID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "chat"
	}

	b := make([]byte, _instanceIDBytes)
	_, _ = rand.Read(b) // crypto/rand never fails on supported platforms

	return host + "-" + hex.EncodeToString(b)
}

// every runs fn on each tick until ctx is canceled (background jobs).
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
