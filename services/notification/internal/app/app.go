// Package app configures and runs notification-svc.
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
	"github.com/minhnn/nexuslive-be/services/notification/config"
	"github.com/minhnn/nexuslive-be/services/notification/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/notification/internal/controller/restapi"
	v1 "github.com/minhnn/nexuslive-be/services/notification/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/notification/internal/mail"
	"github.com/minhnn/nexuslive-be/services/notification/internal/metrics"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo/clients"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo/throttle"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase/notify"
)

const (
	_natsConnectTimeout  = 60 * time.Second
	_redisConnectTimeout = 30 * time.Second
	_backfillRetry       = time.Minute
	_purgeTick           = 24 * time.Hour
	_firstPurge          = time.Minute
)

// Run creates objects via constructors.
func Run(cfg *config.Config, generatedSecret bool) { //nolint:funlen // linear wiring
	l := logger.New(cfg.Log.Level)

	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(cfg.PG.PoolMax))
	if err != nil {
		l.Fatal("notification - Run - postgres.New: %v", err)
	}
	defer pg.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	natsCtx, natsCancel := context.WithTimeout(ctx, _natsConnectTimeout)
	bus, err := eventbus.Connect(natsCtx, cfg.NATS.URL, cfg.App.Name)

	natsCancel()

	if err != nil {
		l.Fatal("notification - Run - eventbus.Connect: %v", err)
	}

	defer bus.Close()

	redisCtx, redisCancel := context.WithTimeout(ctx, _redisConnectTimeout)
	rdb, err := redis.New(redisCtx, cfg.Redis.Addr)

	redisCancel()

	if err != nil {
		l.Fatal("notification - Run - redis.New: %v", err)
	}

	defer rdb.Close()

	m := metrics.New()

	uc, err := newUseCase(cfg, generatedSecret, pg, rdb, m, l)
	if err != nil {
		l.Fatal("notification - Run - use case: %v", err)
	}

	// notification.created / unread_changed leave through the outbox; chat-svc relays them on user:{id}.
	go outbox.New(pg.Pool, bus, l).Run(ctx)

	stop, err := startConsumers(ctx, cfg, bus, uc, m, l)
	if err != nil {
		l.Fatal("notification - Run - consumers: %v", err)
	}
	defer stop()

	startJobs(ctx, cfg, uc, l)

	httpServer := httpserver.New(l, httpserver.Port(cfg.HTTP.Port))
	// Before the routes, so every request is counted (GET /metrics included).
	httpServer.App.Use(httpmetrics.New(m.Registerer()))
	restapi.NewRouter(httpServer.App, v1.UseCases{Center: uc, Settings: uc},
		jwt.NewJWKSVerifier(cfg.Auth.JWKSURL, cfg.Auth.JWTIssuer), l, cfg.Swagger.Enabled)
	httpServer.App.Get("/metrics", adaptor.HTTPHandler(m.Handler()))

	httpServer.Start()
	l.Info("notification - Run - :%s, auth mailer %t, smtp %q", cfg.HTTP.Port, cfg.Mail.AuthMailerEnabled, cfg.Mail.SMTPAddr)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-interrupt:
		l.Info("notification - Run - signal: %s", s.String())
	case err = <-httpServer.Notify():
		l.Error("notification - Run - httpServer.Notify: %v", err)
	}

	cancel()

	if err = httpServer.Shutdown(); err != nil {
		l.Error("notification - Run - httpServer.Shutdown: %v", err)
	}
}

func newUseCase(cfg *config.Config, generatedSecret bool, pg *postgres.Postgres, rdb *redis.Client, m *metrics.Metrics,
	l logger.Interface,
) (*notify.UseCase, error) {
	if generatedSecret {
		l.Warn("notification - NOTIFICATION_UNSUBSCRIBE_SECRET not set: a random one is used, unsubscribe links break on restart")
	}

	mailer, err := newMailer(cfg, l)
	if err != nil {
		return nil, err
	}

	store := persistent.New(pg)

	return notify.New(notify.Repos{
		Notifications: store, Settings: store, Recipients: store, Replica: store, Emails: store,
		Snapshot: clients.NewSnapshots(cfg.Internal.IdentityURL, cfg.Internal.ChannelURL, cfg.Internal.Secret),
		Throttle: throttle.New(rdb), Mailer: mailer,
	}, notify.Config{
		WebBaseURL: cfg.Mail.WebBaseURL, UnsubscribeSecret: cfg.Mail.UnsubscribeSecret,
		LiveCooldown: cfg.Notification.LiveCooldown, LiveEmailCooldown: cfg.Notification.LiveEmailCooldown,
		EmailDailyMax: cfg.Notification.EmailDailyMax, Retention: cfg.Notification.Retention,
		VerifyTTL: cfg.Mail.VerifyTTL, ResetTTL: cfg.Mail.ResetTTL, LockDuration: cfg.Mail.LockDuration,
	}, l, notify.WithObserver(m)), nil
}

func newMailer(cfg *config.Config, l logger.Interface) (*mail.Mailer, error) {
	if cfg.Mail.SMTPAddr == "" {
		l.Warn("notification - NOTIFICATION_SMTP_ADDR not set: e-mails are only logged")

		return mail.New(nil)
	}

	sender, err := mail.NewSMTP(cfg.Mail.SMTPAddr, cfg.Mail.From)
	if err != nil {
		return nil, err
	}

	return mail.New(sender)
}

// startConsumers attaches the three durables (TSD §3.2). The copies are patched with a synced_at guard,
// so the replica consumer runs while the one-off backfill may still be retrying.
func startConsumers(ctx context.Context, cfg *config.Config, bus *eventbus.Bus, uc *notify.UseCase, m *metrics.Metrics,
	l logger.Interface,
) (func(), error) {
	onErr := func(e eventbus.Event, herr error) { l.Warn("notification - event %s %s: %v", e.Type, e.ID, herr) }

	stopReplica, err := bus.Consume(ctx, events.DurableReplica, events.ReplicaTypes(), events.ReplicaHandler(uc), onErr)
	if err != nil {
		return nil, err
	}

	stops := []func(){stopReplica}
	stopAll := func() {
		for _, s := range stops {
			s()
		}
	}

	stopSources, err := bus.Consume(ctx, events.DurableSources, events.SourceTypes(), events.SourcesHandler(uc, m), onErr,
		eventbus.DeliverNew())
	if err != nil {
		stopAll()

		return nil, err
	}

	stops = append(stops, stopSources)

	// Mailer cutover (TSD §9.2): enable here first, then turn identity's mailer off.
	if !cfg.Mail.AuthMailerEnabled {
		if err := bus.DeleteConsumer(ctx, events.DurableAuthMailer); err != nil {
			l.Warn("notification - delete %s: %v", events.DurableAuthMailer, err)
		}

		return stopAll, nil
	}

	stopMail, err := bus.Consume(ctx, events.DurableAuthMailer, events.AuthMailTypes(), events.AuthMailHandler(uc), onErr,
		eventbus.DeliverNew())
	if err != nil {
		stopAll()

		return nil, err
	}

	stops = append(stops, stopMail)

	return stopAll, nil
}

// startJobs runs the backfill, the e-mail worker and the retention job until ctx is canceled.
func startJobs(ctx context.Context, cfg *config.Config, uc *notify.UseCase, l logger.Interface) {
	go runBackfill(ctx, uc, l)
	go every(ctx, cfg.Mail.Tick, cfg.Mail.Tick, func() {
		if _, err := uc.SendDueEmails(ctx); err != nil {
			l.Warn("notification - send e-mails: %v", err)
		}
	})
	go every(ctx, _firstPurge, _purgeTick, func() {
		if err := uc.Purge(ctx); err != nil {
			l.Warn("notification - purge: %v", err)
		}
	})
}

// runBackfill copies the source snapshots once, retrying every minute until it succeeds.
func runBackfill(ctx context.Context, uc *notify.UseCase, l logger.Interface) {
	for {
		err := uc.Backfill(ctx)
		if err == nil {
			return
		}

		l.Error("notification - backfill (retry in %s): %v", _backfillRetry, err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(_backfillRetry):
		}
	}
}

// every runs fn after first and then on each tick until ctx is canceled.
func every(ctx context.Context, first, d time.Duration, fn func()) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(first):
		fn()
	}

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
