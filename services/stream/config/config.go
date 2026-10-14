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
		App     App
		HTTP    HTTP
		Log     Log
		PG      PG
		NATS    NATS
		Auth    Auth
		Redis   Redis
		Stream  Stream
		Swagger Swagger
	}

	// Swagger -.
	Swagger struct {
		// Serves the API docs and a "Try it out" UI at /swagger/index.html.
		Enabled bool `env:"NXL_SWAGGER_ENABLED" envDefault:"true"`
	}

	// App -.
	App struct {
		Name string `env:"STREAM_APP_NAME" envDefault:"stream-svc"`
		Env  string `env:"NXL_ENV" envDefault:"dev"`
	}

	// HTTP -.
	HTTP struct {
		// Public API behind the gateway / Vite proxy.
		Port string `env:"STREAM_HTTP_PORT" envDefault:"8092"`
		// Internal API for media-svc; reachable only inside the Docker network.
		InternalPort   string `env:"STREAM_INTERNAL_PORT" envDefault:"8093"`
		InternalSecret string `env:"NXL_INTERNAL_SECRET"`
	}

	// Log -.
	Log struct {
		Level string `env:"STREAM_LOG_LEVEL" envDefault:"debug"`
	}

	// PG -.
	PG struct {
		PoolMax int    `env:"STREAM_PG_POOL_MAX" envDefault:"6"`
		URL     string `env:"NXL_PG_URL_STREAM,required"`
	}

	// NATS -.
	NATS struct {
		URL string `env:"NATS_URL" envDefault:"nats://guest:guest@localhost:4222/"`
	}

	// Redis -.
	Redis struct {
		// Read only: viewer counts written by chat-svc. Empty = viewerCount is always null.
		Addr string `env:"STREAM_REDIS_ADDR"`
	}

	// Auth -.
	Auth struct {
		JWKSURL   string `env:"STREAM_JWKS_URL" envDefault:"http://localhost:8090/v1/auth/.well-known/jwks.json"`
		JWTIssuer string `env:"IDENTITY_JWT_ISSUER" envDefault:"nexuslive-identity"`
	}

	// Stream -.
	Stream struct {
		// Shown to creators in the Studio.
		RTMPPublicURL string `env:"STREAM_RTMP_PUBLIC_URL" envDefault:"rtmp://localhost:1935/live"`
		// Prefix viewers use for HLS objects (Vite proxy /hls → MinIO bucket).
		HLSBase string `env:"STREAM_HLS_BASE" envDefault:"/hls"`
		// NXL-A §7A: how long an interrupted stream waits for OBS before it ends.
		GracePeriod  time.Duration `env:"STREAM_INGEST_GRACE_PERIOD" envDefault:"60s"`
		VODRetention time.Duration `env:"STREAM_VOD_RETENTION" envDefault:"336h"`
		ExpiryTick   time.Duration `env:"STREAM_EXPIRY_TICK" envDefault:"5s"`
		// After the creator ends a stream, publishes are refused this long so OBS auto-reconnect
		// does not start a new broadcast. 0 disables it.
		EndCooldown   time.Duration `env:"STREAM_END_COOLDOWN" envDefault:"30s"`
		VODExpiryTick time.Duration `env:"STREAM_VOD_EXPIRY_TICK" envDefault:"10m"`
	}
)

// ErrInternalSecretRequired -.
var ErrInternalSecretRequired = errors.New("NXL_INTERNAL_SECRET is required outside dev")

// _devInternalSecret keeps `make nxl-up` working without an .env; refused outside dev.
const _devInternalSecret = "dev-internal-secret" //nolint:gosec // dev-only default, refused outside dev

// IsDev -.
func (c *Config) IsDev() bool {
	return c.App.Env == "dev"
}

// NewConfig returns app config.
func NewConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	if cfg.HTTP.InternalSecret == "" {
		if !cfg.IsDev() {
			return nil, fmt.Errorf("config error: %w", ErrInternalSecretRequired)
		}

		cfg.HTTP.InternalSecret = _devInternalSecret
	}

	return cfg, nil
}
