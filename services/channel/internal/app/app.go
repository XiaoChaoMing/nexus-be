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
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/pkg/outbox"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/channel/config"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi"
	v1 "github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/channel/internal/imaging"
	"github.com/minhnn/nexuslive-be/services/channel/internal/metrics"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo/objects"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo/viewers"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/channels"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/schedules"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/search"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/studio"
)

const (
	_natsConnectTimeout  = 60 * time.Second
	_redisConnectTimeout = 30 * time.Second
	_bucketRetries       = 30
	_bucketRetryDelay    = 2 * time.Second
	// Multipart overhead on top of the largest image (banner, 5 MB).
	_bodyLimit = 6 << 20
)

type useCases struct {
	channels  *channels.UseCase
	studio    *studio.UseCase
	search    *search.UseCase
	schedules *schedules.UseCase
}

// Run creates objects via constructors.
func Run(cfg *config.Config) { //nolint:funlen,gocyclo,cyclop // linear wiring: one branch per fatal start-up error
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

	var counts repo.ViewerCountRepo

	if cfg.Redis.Addr != "" {
		redisCtx, redisCancel := context.WithTimeout(ctx, _redisConnectTimeout)
		rdb, rerr := redis.New(redisCtx, cfg.Redis.Addr)

		redisCancel()

		if rerr != nil {
			l.Fatal("channel - Run - redis.New: %v", rerr)
		}

		defer rdb.Close()

		counts = viewers.New(rdb)
	}

	store, err := objects.New(cfg.S3.Endpoint, cfg.S3.AccessKey, cfg.S3.SecretKey, cfg.S3.Bucket, cfg.S3.UseSSL)
	if err != nil {
		l.Fatal("channel - Run - objects: %v", err)
	}

	if cfg.S3.EnsureBucket {
		if err = ensureBucket(ctx, store, l); err != nil {
			l.Fatal("channel - Run - ensure bucket: %v", err)
		}
	}

	uc := wire(cfg, persistent.New(pg), store, counts, l)
	m := metrics.New()

	// Covers are placeholders: a failure only leaves broken images, so it does not stop the service.
	if n, cerr := uc.studio.EnsureCategoryCovers(ctx); cerr != nil {
		l.Warn("channel - category covers: %v", cerr)
	} else if n > 0 {
		l.Info("channel - generated %d category covers", n)
	}

	// Outgoing events leave through the outbox.
	go outbox.New(pg.Pool, bus, l).Run(ctx)

	stopConsumer, err := bus.Consume(ctx, events.Durable, events.Types, events.Handler(uc.channels), func(e eventbus.Event, herr error) {
		l.Warn("channel - event %s %s: %v", e.Type, e.ID, herr)
	})
	if err != nil {
		l.Fatal("channel - Run - consume: %v", err)
	}
	defer stopConsumer()

	startJobs(ctx, cfg, uc, m, l)

	public := httpserver.New(l, httpserver.Port(cfg.HTTP.Port), httpserver.BodyLimit(_bodyLimit))
	// Before the routes, so every request is counted (GET /metrics included).
	public.App.Use(httpmetrics.New(m.Registerer()))
	restapi.NewRouter(public.App, v1.UseCases{
		Directory: uc.channels, Follows: uc.channels, Studio: uc.studio, Catalog: uc.studio, Search: uc.search,
		Schedules: uc.schedules, Reminders: uc.schedules,
	}, jwt.NewJWKSVerifier(cfg.Auth.JWKSURL, cfg.Auth.JWTIssuer), v1.Options{
		AvatarMaxBytes: cfg.Channel.AvatarMaxBytes, BannerMaxBytes: cfg.Channel.BannerMaxBytes,
		ScheduleWindow: cfg.Schedule.Window, ScheduleHorizon: cfg.Schedule.Horizon,
	}, l, cfg.Swagger.Enabled)
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

func wire(cfg *config.Config, store *persistent.Repo, objs repo.ObjectStore, counts repo.ViewerCountRepo, l logger.Interface) useCases {
	e := enrich.New(store, counts, l)

	return useCases{
		channels: channels.New(channels.Repos{
			Channels: store, Follows: store, Schedules: store, Reminders: store, Search: store,
		}, e, l),
		studio: studio.New(studio.Repos{
			Channels: store, Follows: store, Schedules: store, Reminders: store, Categories: store, Objects: objs,
		}, imaging.New(), e, studio.Config{
			HandleChangeInterval: cfg.Channel.HandleChangeInterval,
			AvatarMaxBytes:       cfg.Channel.AvatarMaxBytes,
			BannerMaxBytes:       cfg.Channel.BannerMaxBytes,
			MediaBase:            cfg.S3.MediaBase,
			Horizon:              cfg.Schedule.Horizon,
		}, l),
		search: search.New(store, store, e, l),
		schedules: schedules.New(schedules.Repos{
			Channels: store, Schedules: store, Reminders: store, Categories: store,
		}, e, schedules.Config{
			MaxActive:       cfg.Schedule.MaxActive,
			Horizon:         cfg.Schedule.Horizon,
			UpcomingLead:    cfg.Schedule.UpcomingLead,
			ReminderLead:    cfg.Schedule.ReminderLead,
			DefaultTimezone: cfg.Schedule.DefaultTimezone,
		}, l),
	}
}

// startJobs runs the background jobs until ctx is canceled; a failed run is logged, counted and retried next tick.
func startJobs(ctx context.Context, cfg *config.Config, uc useCases, m *metrics.Metrics, l logger.Interface) {
	if cfg.Schedule.Tick > 0 {
		go every(ctx, cfg.Schedule.Tick, func() {
			runJob(ctx, "announce_upcoming", uc.schedules.AnnounceUpcoming, m, l)
			runJob(ctx, "due_reminders", uc.schedules.DueReminders, m, l)
		})
	}

	if cfg.Channel.SearchViewerTick > 0 {
		go every(ctx, cfg.Channel.SearchViewerTick, func() {
			if err := uc.search.RefreshViewers(ctx); err != nil {
				m.JobFailed("search_viewers")
				l.Warn("channel - search viewers: %v", err)
			}
		})
	}
}

func runJob(ctx context.Context, name string, job func(context.Context) (int, error), m *metrics.Metrics, l logger.Interface) {
	n, err := job(ctx)
	if err != nil {
		m.JobFailed(name)
		l.Warn("channel - job %s: %v", name, err)

		return
	}

	if n > 0 {
		l.Info("channel - job %s: %d", name, n)
	}
}

// ensureBucket retries because MinIO may still be starting under compose.
func ensureBucket(ctx context.Context, store *objects.Store, l logger.Interface) error {
	var err error

	for i := range _bucketRetries {
		attempt, cancel := context.WithTimeout(ctx, _bucketRetryDelay*2) //nolint:mnd // two delays per attempt
		err = store.EnsureBucket(attempt)

		cancel()

		if err == nil {
			return nil
		}

		l.Warn("channel - storage not ready (attempt %d/%d): %v", i+1, _bucketRetries, err)
		time.Sleep(_bucketRetryDelay)
	}

	return err
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
