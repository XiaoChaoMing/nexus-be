package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type (
	// Config is read from the environment only.
	Config struct {
		App      App
		HTTP     HTTP
		Log      Log
		PG       PG
		NATS     NATS
		Auth     Auth
		Redis    Redis
		S3       S3
		Channel  Channel
		Schedule Schedule
		Swagger  Swagger
	}

	// Swagger -.
	Swagger struct {
		// Serves the API docs and a "Try it out" UI at /swagger/index.html.
		Enabled bool `env:"NXL_SWAGGER_ENABLED" envDefault:"true"`
	}

	// App -.
	App struct {
		Name string `env:"CHANNEL_APP_NAME" envDefault:"channel-svc"`
		Env  string `env:"NXL_ENV" envDefault:"dev"`
	}

	// HTTP -.
	HTTP struct {
		Port string `env:"CHANNEL_HTTP_PORT" envDefault:"8094"`
		// Internal API (channel and follow snapshots for other services); reachable only inside the Docker network.
		InternalPort   string `env:"CHANNEL_INTERNAL_PORT" envDefault:"8088"`
		InternalSecret string `env:"NXL_INTERNAL_SECRET"`
	}

	// Log -.
	Log struct {
		Level string `env:"CHANNEL_LOG_LEVEL" envDefault:"debug"`
	}

	// PG -.
	PG struct {
		PoolMax int    `env:"CHANNEL_PG_POOL_MAX" envDefault:"4"`
		URL     string `env:"NXL_PG_URL_CHANNEL,required"`
	}

	// NATS -.
	NATS struct {
		URL string `env:"NATS_URL" envDefault:"nats://guest:guest@localhost:4222/"`
	}

	// Auth verifies identity-svc access tokens locally (JWKS).
	Auth struct {
		JWKSURL   string `env:"CHANNEL_JWKS_URL"    envDefault:"http://localhost:8090/v1/auth/.well-known/jwks.json"`
		JWTIssuer string `env:"IDENTITY_JWT_ISSUER" envDefault:"nexuslive-identity"`
	}

	// Redis -.
	Redis struct {
		// Read only: viewer counts written by chat-svc. Empty = viewerCount is always null.
		Addr string `env:"CHANNEL_REDIS_ADDR"`
	}

	// S3 is the public bucket of avatars, banners and category covers (served by the edge under /media/).
	S3 struct {
		Endpoint     string `env:"CHANNEL_S3_ENDPOINT"      envDefault:"minio:9000"`
		AccessKey    string `env:"CHANNEL_S3_ACCESS_KEY,required"`
		SecretKey    string `env:"CHANNEL_S3_SECRET_KEY,required"`
		UseSSL       bool   `env:"CHANNEL_S3_USE_SSL"       envDefault:"false"`
		Bucket       string `env:"CHANNEL_PUBLIC_BUCKET"    envDefault:"nxl-public"`
		EnsureBucket bool   `env:"CHANNEL_S3_ENSURE_BUCKET" envDefault:"true"`
		// MediaBase is the URL prefix of the bucket at the edge; stored image URLs start with it.
		MediaBase string `env:"CHANNEL_MEDIA_BASE" envDefault:"/media"`
	}

	// Channel holds the profile limits (NXL-00 §12).
	Channel struct {
		AvatarMaxBytes       int           `env:"CHANNEL_AVATAR_MAX_BYTES"       envDefault:"2097152"`
		BannerMaxBytes       int           `env:"CHANNEL_BANNER_MAX_BYTES"       envDefault:"5242880"`
		HandleChangeInterval time.Duration `env:"CHANNEL_HANDLE_CHANGE_INTERVAL" envDefault:"720h"`
		SearchViewerTick     time.Duration `env:"CHANNEL_SEARCH_VIEWER_TICK"     envDefault:"30s"`
	}

	// Schedule holds the broadcast schedule limits and job cadence (NXL-G2, NXL-00 §12).
	Schedule struct {
		DefaultTimezone string `env:"CHANNEL_DEFAULT_TIMEZONE" envDefault:"Asia/Ho_Chi_Minh"`
		// Tick of the upcoming / reminder jobs; 0 turns both off.
		Tick         time.Duration `env:"CHANNEL_SCHEDULE_TICK"          envDefault:"1m"`
		UpcomingLead time.Duration `env:"CHANNEL_SCHEDULE_UPCOMING_LEAD" envDefault:"24h"`
		MaxActive    int           `env:"CHANNEL_SCHEDULE_MAX_ACTIVE"    envDefault:"20"`
		Horizon      time.Duration `env:"CHANNEL_SCHEDULE_HORIZON"       envDefault:"2160h"`
		Window       time.Duration `env:"CHANNEL_SCHEDULE_WINDOW"        envDefault:"336h"`
		ReminderLead time.Duration `env:"CHANNEL_REMINDER_LEAD"          envDefault:"10m"`
	}
)

// ErrInternalSecretRequired -.
var ErrInternalSecretRequired = errors.New("NXL_INTERNAL_SECRET is required outside dev")

// _devInternalSecret keeps `make nxl-up` working without an .env; refused outside dev.
const _devInternalSecret = "dev-internal-secret" //nolint:gosec // dev-only default, refused outside dev

// NewConfig returns app config.
func NewConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	if cfg.HTTP.InternalSecret == "" {
		if cfg.App.Env != "dev" {
			return nil, fmt.Errorf("config error: %w", ErrInternalSecretRequired)
		}

		cfg.HTTP.InternalSecret = _devInternalSecret
	}

	return cfg, nil
}
