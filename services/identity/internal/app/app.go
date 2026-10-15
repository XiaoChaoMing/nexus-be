// Package app configures and runs identity-svc.
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
	"github.com/minhnn/nexuslive-be/services/identity/config"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/internalapi"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi"
	v1 "github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/mailer"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase/auth"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase/bootstrap"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase/creator"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase/moderation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	_natsConnectTimeout = 60 * time.Second
	_seedTimeout        = 30 * time.Second
	_cliTimeout         = 30 * time.Second
)

// useCases are the wired application services.
type useCases struct {
	auth       *auth.UseCase
	creator    *creator.UseCase
	bootstrap  *bootstrap.UseCase
	moderation *moderation.UseCase
}

// Run creates objects via constructors.
func Run(cfg *config.Config) { //nolint:funlen,gocyclo,cyclop // linear wiring: one branch per fatal start-up error
	l := logger.New(cfg.Log.Level)

	signer, err := newSigner(cfg, l)
	if err != nil {
		l.Fatal("identity - Run - newSigner: %v", err)
	}

	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(cfg.PG.PoolMax))
	if err != nil {
		l.Fatal("identity - Run - postgres.New: %v", err)
	}
	defer pg.Close()

	uc, err := newUseCases(cfg, pg, signer, l)
	if err != nil {
		l.Fatal("identity - Run - newUseCases: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seedDev(ctx, cfg, uc.bootstrap, l)

	natsCtx, natsCancel := context.WithTimeout(ctx, _natsConnectTimeout)
	bus, err := eventbus.Connect(natsCtx, cfg.NATS.URL, cfg.App.Name)

	natsCancel()

	if err != nil {
		l.Fatal("identity - Run - eventbus.Connect: %v", err)
	}

	defer bus.Close()

	go outbox.New(pg.Pool, bus, l).Run(ctx)

	stopConsumers, err := startConsumers(ctx, cfg, bus, uc, l)
	if err != nil {
		l.Fatal("identity - Run - startConsumers: %v", err)
	}
	defer stopConsumers()

	httpServer := httpserver.New(l, httpserver.Port(cfg.HTTP.Port))
	reg := newMetricsRegistry()
	// Before the routes, so every request is counted (GET /metrics included).
	httpServer.App.Use(httpmetrics.New(reg))
	httpServer.App.Get("/metrics", adaptor.HTTPHandler(promhttp.HandlerFor(reg, promhttp.HandlerOpts{})))
	restapi.NewRouter(httpServer.App, v1.Deps{
		Auth: uc.auth, Creator: uc.creator, Blocks: uc.moderation, AdminUsers: uc.moderation,
		Signer: signer, Logger: l, CookieSecure: cfg.Cookies.Secure,
	}, cfg.Swagger.Enabled)

	internal := httpserver.New(l, httpserver.Port(cfg.HTTP.InternalPort))
	internalapi.NewRouter(internal.App, uc.moderation, cfg.HTTP.InternalSecret, l)

	httpServer.Start()
	internal.Start()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case s := <-interrupt:
		l.Info("identity - Run - signal: %s", s.String())
	case err = <-httpServer.Notify():
		l.Error("identity - Run - httpServer.Notify: %v", err)
	case err = <-internal.Notify():
		l.Error("identity - Run - internal.Notify: %v", err)
	}

	if err = httpServer.Shutdown(); err != nil {
		l.Error("identity - Run - httpServer.Shutdown: %v", err)
	}

	if err = internal.Shutdown(); err != nil {
		l.Error("identity - Run - internal.Shutdown: %v", err)
	}
}

// PromoteAdmin implements `identity promote-admin <email>` and returns the process exit code.
func PromoteAdmin(cfg *config.Config, email string) int {
	l := logger.New(cfg.Log.Level)

	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(1))
	if err != nil {
		l.Error("identity - promote-admin - postgres.New: %v", err)

		return 1
	}
	defer pg.Close()

	ctx, cancel := context.WithTimeout(context.Background(), _cliTimeout)
	defer cancel()

	uc := bootstrap.New(persistent.NewSeedRepo(pg), persistent.NewUserRepo(pg), auth.NewArgon2(auth.DefaultArgon2Params), l)

	u, err := uc.PromoteAdmin(ctx, email)
	if err != nil {
		l.Error("identity - promote-admin: %v", err)

		return 1
	}

	l.Info("identity - promote-admin: %s is now ADMIN (effective at the next token refresh)", u.Username)

	return 0
}

func newUseCases(cfg *config.Config, pg *postgres.Postgres, signer *jwt.EdDSA, l logger.Interface) (useCases, error) {
	loc := referenceLocation(l)
	users := persistent.NewUserRepo(pg)
	hasher := auth.NewArgon2(auth.DefaultArgon2Params)

	authUC, err := auth.New(auth.Repos{
		Users:    users,
		Sessions: persistent.NewSessionRepo(pg),
		Tokens:   persistent.NewTokenRepo(pg),
	}, hasher, signer, auth.Config{
		RefreshTTL:        cfg.Auth.RefreshTTL,
		EmailVerifyTTL:    cfg.Account.EmailVerifyTTL,
		PasswordResetTTL:  cfg.Account.PasswordResetTTL,
		LoginMaxFailures:  cfg.Account.LoginMaxFailures,
		LoginLockDuration: cfg.Account.LoginLockDuration,
		Location:          loc,
	}, l)
	if err != nil {
		return useCases{}, err
	}

	return useCases{
		auth: authUC,
		creator: creator.New(persistent.NewApplicationRepo(pg), users, persistent.NewChannelRepo(pg), creator.Config{
			RequireVerifiedEmail: cfg.Account.RequireVerifiedForCreator,
			MinAge:               cfg.Account.CreatorMinAge,
			Location:             loc,
		}, l),
		bootstrap: bootstrap.New(persistent.NewSeedRepo(pg), users, hasher, l),
		moderation: moderation.New(moderation.Repos{
			Users: users, Blocks: persistent.NewBlockRepo(pg), Actions: persistent.NewModerationRepo(pg),
			Snapshot: persistent.NewModerationRepo(pg),
		}, l),
	}, nil
}

// referenceLocation is the calendar used for ages and "not in the future" dates (the platform's
// home time zone); time/tzdata is embedded by cmd/identity, so it loads in the scratch image too.
func referenceLocation(l logger.Interface) *time.Location {
	loc, err := time.LoadLocation(entity.DefaultTimezone)
	if err != nil {
		l.Warn("identity - time zone %s unavailable, using UTC: %v", entity.DefaultTimezone, err)

		return time.UTC
	}

	return loc
}

func seedDev(ctx context.Context, cfg *config.Config, uc *bootstrap.UseCase, l logger.Interface) {
	if !cfg.IsDev() || !cfg.Seed.Enabled {
		return
	}

	seedCtx, cancel := context.WithTimeout(ctx, _seedTimeout)
	defer cancel()

	if err := uc.SeedDev(seedCtx, cfg.Seed.Password); err != nil {
		l.Error("identity - dev seed: %v", err)
	}
}

func startConsumers(ctx context.Context, cfg *config.Config, bus *eventbus.Bus, uc useCases, l logger.Interface) (func(), error) {
	onErr := func(e eventbus.Event, herr error) {
		l.Warn("identity - event %s %s: %v", e.Type, e.ID, herr)
	}

	stopChannels, err := bus.Consume(ctx, events.DurableChannels, events.Types(), events.Handler(uc.creator, uc.moderation), onErr)
	if err != nil {
		return nil, err
	}

	// Mailer cutover (GĐ5 TSD §9.2): notification-svc takes over first, then this consumer is switched off
	// and its durable deleted; turned back on, it is recreated at the next message (no old e-mail resent).
	if !cfg.Mail.Enabled {
		if err := bus.DeleteConsumer(ctx, events.DurableMailer); err != nil {
			l.Warn("identity - delete %s: %v", events.DurableMailer, err)
		}

		l.Info("identity - IDENTITY_MAILER_ENABLED=false: account e-mails are sent by notification-svc")

		return stopChannels, nil
	}

	m, err := newMailer(cfg, l)
	if err != nil {
		stopChannels()

		return nil, err
	}

	stopMailer, err := bus.Consume(ctx, events.DurableMailer, events.MailTypes(), events.MailHandler(m), onErr,
		eventbus.DeliverNew())
	if err != nil {
		stopChannels()

		return nil, err
	}

	return func() {
		stopMailer()
		stopChannels()
	}, nil
}

func newMailer(cfg *config.Config, l logger.Interface) (*mailer.Mailer, error) {
	mcfg := mailer.Config{
		WebBaseURL: cfg.Mail.WebBaseURL,
		VerifyTTL:  cfg.Account.EmailVerifyTTL,
		ResetTTL:   cfg.Account.PasswordResetTTL,
	}

	if cfg.Mail.SMTPAddr == "" {
		l.Warn("identity - IDENTITY_SMTP_ADDR not set: account e-mails are not sent")

		return mailer.New(mcfg, nil, l)
	}

	sender, err := mailer.NewSMTP(cfg.Mail.SMTPAddr, cfg.Mail.From)
	if err != nil {
		return nil, err
	}

	return mailer.New(mcfg, sender, l)
}

func newSigner(cfg *config.Config, l logger.Interface) (*jwt.EdDSA, error) {
	if cfg.Auth.JWTPrivateKey != "" {
		return jwt.NewEdDSA(cfg.Auth.JWTPrivateKey, cfg.Auth.JWTIssuer, cfg.Auth.AccessTTL)
	}

	if !cfg.IsDev() {
		l.Fatal("identity - IDENTITY_JWT_PRIVATE_KEY is required outside dev")
	}

	l.Warn("identity - IDENTITY_JWT_PRIVATE_KEY not set: using a random key, access tokens will not survive a restart")

	return jwt.GenerateEdDSA(cfg.Auth.JWTIssuer, cfg.Auth.AccessTTL)
}

// newMetricsRegistry is served at /metrics (scraped by Prometheus inside the compose network only).
func newMetricsRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	return reg
}
