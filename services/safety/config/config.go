// Package config reads safety-svc settings from the environment.
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
		Redis    Redis
		Auth     Auth
		Internal Internal
		Safety   Safety
		Swagger  Swagger
	}

	// Swagger -.
	Swagger struct {
		Enabled bool `env:"NXL_SWAGGER_ENABLED" envDefault:"true"`
	}

	// App -.
	App struct {
		Name string `env:"SAFETY_APP_NAME" envDefault:"safety-svc"`
		Env  string `env:"NXL_ENV"         envDefault:"dev"`
	}

	// HTTP -.
	HTTP struct {
		Port string `env:"SAFETY_HTTP_PORT" envDefault:"8099"`
	}

	// Log -.
	Log struct {
		Level string `env:"SAFETY_LOG_LEVEL" envDefault:"debug"`
	}

	// PG -.
	PG struct {
		PoolMax int    `env:"SAFETY_PG_POOL_MAX"         envDefault:"6"`
		URL     string `env:"NXL_PG_URL_SAFETY,required"`
	}

	// NATS -.
	NATS struct {
		URL string `env:"NATS_URL" envDefault:"nats://guest:guest@localhost:4222/"`
	}

	// Redis holds the report rate limits.
	Redis struct {
		Addr string `env:"SAFETY_REDIS_ADDR" envDefault:"localhost:6379"`
	}

	// Auth -.
	Auth struct {
		JWKSURL   string `env:"SAFETY_JWKS_URL"     envDefault:"http://localhost:8090/v1/auth/.well-known/jwks.json"`
		JWTIssuer string `env:"IDENTITY_JWT_ISSUER" envDefault:"nexuslive-identity"`
	}

	// Internal are the internal APIs safety-svc calls: chat-svc for report evidence, identity / channel /
	// stream for the one-off backfill of its copies.
	Internal struct {
		Secret          string        `env:"NXL_INTERNAL_SECRET"`
		ChatURL         string        `env:"SAFETY_CHAT_INTERNAL_URL"     envDefault:"http://localhost:8085"`
		EvidenceTimeout time.Duration `env:"SAFETY_EVIDENCE_TIMEOUT"      envDefault:"300ms"`
		IdentityURL     string        `env:"SAFETY_IDENTITY_INTERNAL_URL" envDefault:"http://localhost:8089"`
		ChannelURL      string        `env:"SAFETY_CHANNEL_INTERNAL_URL"  envDefault:"http://localhost:8088"`
		StreamURL       string        `env:"SAFETY_STREAM_INTERNAL_URL"   envDefault:"http://localhost:8093"`
	}

	// Safety are the NXL-F values.
	Safety struct {
		// ReportRateLimit reports per user and hour (REPORT_RATE_LIMIT).
		ReportRateLimit int `env:"SAFETY_REPORT_RATE_LIMIT" envDefault:"10"`
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

	if cfg.Internal.Secret == "" {
		if cfg.App.Env != "dev" {
			return nil, fmt.Errorf("config error: %w", ErrInternalSecretRequired)
		}

		cfg.Internal.Secret = _devInternalSecret
	}

	return cfg, nil
}
