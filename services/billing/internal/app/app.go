// Package app configures and runs billing-svc and its CLI commands.
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
	"github.com/minhnn/nexuslive-be/services/billing/config"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/devpsp"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi"
	v1 "github.com/minhnn/nexuslive-be/services/billing/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/billing/internal/controller/webhook"
	"github.com/minhnn/nexuslive-be/services/billing/internal/entity"
	"github.com/minhnn/nexuslive-be/services/billing/internal/metrics"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/cipher"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/fake"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/provider/sepay"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/redisstore"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo/snapshot"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase/billing"
)

const (
	_natsConnectTimeout  = 60 * time.Second
	_redisConnectTimeout = 30 * time.Second
	_expiryTick          = time.Minute
	_dailyTick           = 5 * time.Minute
	_purgeTick           = time.Hour
	_businessTZ          = "Asia/Ho_Chi_Minh"
)

// deps are the objects the server and the CLI share.
type deps struct {
	pg    *postgres.Postgres
	rdb   *redis.Client
	store *persistent.Repo
	rs    *redisstore.Store
	prov  provider.PaymentProvider
	fake  *fake.Provider
	uc    *billing.UseCase
	m     *metrics.Metrics
}

func (d *deps) close() {
	d.rdb.Close()
	d.pg.Close()
}

// build connects Postgres and Redis and wires the use cases; ctx bounds the Redis connection attempts.
func build(ctx context.Context, cfg *config.Config, l logger.Interface) (*deps, error) {
	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(cfg.PG.PoolMax))
	if err != nil {
		return nil, err
	}

	redisCtx, cancel := context.WithTimeout(ctx, _redisConnectTimeout)
	rdb, err := redis.New(redisCtx, cfg.Redis.Addr)

	cancel()

	if err != nil {
		pg.Close()

		return nil, err
	}

	loc, err := time.LoadLocation(_businessTZ)
	if err != nil {
		loc = time.UTC
	}

	d := &deps{pg: pg, rdb: rdb, store: persistent.New(pg), rs: redisstore.New(rdb.Client), m: metrics.New()}

	var opts []billing.Option

	switch cfg.Billing.Provider {
	case config.ProviderFake:
		d.fake = fake.New(fake.Config{
			PSPURL: cfg.Fake.PSPURL, IPNURL: cfg.Fake.IPNURL, SecretKey: cfg.SePay.SecretKey,
			IPNSecret: cfg.SePay.IPNSecret,
		}, d.rs)
		d.prov = d.fake
		opts = append(opts, billing.WithFake(d.fake))
	default:
		d.prov = sepay.New(sepay.Config{
			CheckoutURL: cfg.SePay.CheckoutURL, APIURL: cfg.SePay.APIURL, MerchantID: cfg.SePay.MerchantID,
			SecretKey: cfg.SePay.SecretKey, IPNSecret: cfg.SePay.IPNSecret, Timeout: cfg.SePay.APITimeout, Location: loc,
		}, d.m, l)
	}

	c, err := cipher.New(cfg.Billing.PayoutEncKey)
	if err != nil {
		d.close()

		return nil, err
	}

	d.uc = billing.New(billing.Repos{
		Channels: d.store, Tiers: d.store, Orders: d.store, Subs: d.store, Ledger: d.store, Payouts: d.store,
		Audit: d.store, Txns: d.store, Idem: d.store, Lock: d.rs, Sync: d.rs, Cipher: c,
		Snapshot: snapshot.New(cfg.Internal.ChannelURL, cfg.Internal.Secret),
	}, d.prov, settings(cfg, loc), l, d.m, opts...)

	return d, nil
}

func settings(cfg *config.Config, loc *time.Location) billing.Settings {
	methods := make([]entity.PaymentMethod, 0, len(cfg.SePay.Methods))
	for _, m := range cfg.SePay.Methods {
		methods = append(methods, entity.PaymentMethod(m))
	}

	return billing.Settings{
		TierPrices: cfg.Billing.Prices, FeeBps: cfg.Billing.FeeBps, PayoutMin: cfg.Billing.PayoutMin,
		OrderTTL: cfg.Billing.OrderTTL, ReconcileAfter: cfg.Billing.ReconcileAfter,
		ReconcileInterval: cfg.Billing.ReconcileInterval, ReconcileGiveUp: cfg.Billing.ReconcileGiveUp,
		RenewReminderDays: cfg.Billing.RenewReminderDays, Methods: methods, WebBaseURL: cfg.Billing.WebBaseURL,
		VerifyViaAPI: cfg.SePay.VerifyViaAPI, Location: loc,
	}
}

// Run starts the HTTP server, the outbox relay, the channel consumer and the jobs.
func Run(cfg *config.Config) { //nolint:funlen // linear wiring
	l := logger.New(cfg.Log.Level)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := build(ctx, cfg, l)
	if err != nil {
		l.Fatal("billing - Run - build: %v", err)
	}
	defer d.close()

	natsCtx, natsCancel := context.WithTimeout(ctx, _natsConnectTimeout)
	bus, err := eventbus.Connect(natsCtx, cfg.NATS.URL, cfg.App.Name)

	natsCancel()

	if err != nil {
		l.Fatal("billing - Run - eventbus.Connect: %v", err)
	}

	defer bus.Close()

	// subscription.*, payment.* and audit.recorded leave through the outbox.
	go outbox.New(d.pg.Pool, bus, l).Run(ctx)

	stop, err := bus.Consume(ctx, events.Durable, events.Types(), events.Handler(d.uc), func(e eventbus.Event, herr error) {
		l.Warn("billing - event %s %s: %v", e.Type, e.ID, herr)
	})
	if err != nil {
		l.Fatal("billing - Run - consume: %v", err)
	}
	defer stop()

	startJobs(ctx, cfg, d.uc, l)

	httpServer := httpserver.New(l, httpserver.Port(cfg.HTTP.Port))
	// Before the routes, so every request is counted (GET /metrics included).
	httpServer.App.Use(httpmetrics.New(d.m.Registerer()))
	restapi.NewRouter(httpServer.App, v1.UseCases{
		Catalog: d.uc, Checkout: d.uc, Subscriptions: d.uc, Studio: d.uc, Admin: d.uc, Idempotency: d.uc,
	}, jwt.NewJWKSVerifier(cfg.Auth.JWKSURL, cfg.Auth.JWTIssuer), l, cfg.Swagger.Enabled)
	webhook.Register(httpServer.App, d.uc, l)

	if d.fake != nil {
		devpsp.Register(httpServer.App, d.uc, l)
	}

	httpServer.App.Get("/metrics", adaptor.HTTPHandler(d.m.Handler()))

	httpServer.Start()
	l.Info("billing - Run - :%s, provider %s, methods %v, fee %d bps", cfg.HTTP.Port, d.prov.Name(), cfg.SePay.Methods,
		cfg.Billing.FeeBps)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-interrupt:
		l.Info("billing - Run - signal: %s", s.String())
	case err = <-httpServer.Notify():
		l.Error("billing - Run - httpServer.Notify: %v", err)
	}

	cancel()

	if err = httpServer.Shutdown(); err != nil {
		l.Error("billing - Run - httpServer.Shutdown: %v", err)
	}

	d.uc.Wait()
}

// startJobs runs the periodic jobs until ctx is canceled; each takes a Redis lock so one instance runs it.
func startJobs(ctx context.Context, cfg *config.Config, uc *billing.UseCase, l logger.Interface) {
	go every(ctx, cfg.Billing.ReconcileInterval, func() {
		if n, err := uc.ReconcilePending(ctx); err != nil {
			l.Warn("billing - reconcile-pending: %v", err)
		} else if n > 0 {
			l.Info("billing - reconcile-pending: %d orders changed", n)
		}
	})
	go every(ctx, _expiryTick, func() {
		if reminded, expired, err := uc.SubscriptionExpiry(ctx); err != nil {
			l.Warn("billing - subscription-expiry: %v", err)
		} else if reminded+expired > 0 {
			l.Info("billing - subscription-expiry: %d reminded, %d expired", reminded, expired)
		}
	})
	go every(ctx, _dailyTick, func() {
		if ran, rep, err := uc.ReconcileDailyIfDue(ctx); err != nil {
			l.Warn("billing - reconcile-daily: %v", err)
		} else if ran {
			l.Info("billing - reconcile-daily: %d listed, %d applied, %d mismatches", rep.Listed, rep.Applied, rep.Mismatches)
		}
	})
	go every(ctx, _purgeTick, func() {
		if _, err := uc.PurgeIdempotency(ctx); err != nil {
			l.Warn("billing - idempotency-purge: %v", err)
		}
	})
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
