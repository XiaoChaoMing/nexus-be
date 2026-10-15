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
		Auth    Auth
		Cookies Cookies
		NATS    NATS
		Swagger Swagger
		Mail    Mail
		Account Account
		Seed    Seed
	}

	// Mail -.
	Mail struct {
		// Empty disables sending: the identity-mailer consumer only logs that a mail was requested.
		SMTPAddr string `env:"IDENTITY_SMTP_ADDR"`
		From     string `env:"IDENTITY_MAIL_FROM" envDefault:"NexusLive <no-reply@nexuslive.dev>"`
		// Links in e-mails point here: {WebBaseURL}/verify-email?token=..., {WebBaseURL}/reset-password?token=...
		WebBaseURL string `env:"IDENTITY_WEB_BASE_URL" envDefault:"http://localhost:5173"`
	}

	// Account -.
	Account struct {
		EmailVerifyTTL   time.Duration `env:"IDENTITY_EMAIL_VERIFY_TTL" envDefault:"24h"`
		PasswordResetTTL time.Duration `env:"IDENTITY_PASSWORD_RESET_TTL" envDefault:"30m"`
		// LoginMaxFailures wrong passwords within LoginLockDuration lock the account for LoginLockDuration.
		LoginMaxFailures          int           `env:"IDENTITY_LOGIN_MAX_FAILURES" envDefault:"10"`
		LoginLockDuration         time.Duration `env:"IDENTITY_LOGIN_LOCK_DURATION" envDefault:"15m"`
		RequireVerifiedForCreator bool          `env:"IDENTITY_REQUIRE_VERIFIED_FOR_CREATOR" envDefault:"true"`
		CreatorMinAge             int           `env:"IDENTITY_CREATOR_MIN_AGE" envDefault:"18"`
	}

	// Seed -.
	Seed struct {
		// Creates the admin and creator demo accounts at startup; only effective when NXL_ENV=dev.
		Enabled  bool   `env:"IDENTITY_DEV_SEED" envDefault:"true"`
		Password string `env:"IDENTITY_DEV_SEED_PASSWORD" envDefault:"nexuslive123"`
	}

	// Swagger -.
	Swagger struct {
		// Serves the API docs and a "Try it out" UI at /swagger/index.html.
		Enabled bool `env:"NXL_SWAGGER_ENABLED" envDefault:"true"`
	}

	// NATS -.
	NATS struct {
		// Outbox events (user.registered.v1) are published here.
		URL string `env:"NATS_URL" envDefault:"nats://guest:guest@localhost:4222/"`
	}

	// App -.
	App struct {
		Name    string `env:"IDENTITY_APP_NAME" envDefault:"identity-svc"`
		Version string `env:"IDENTITY_APP_VERSION" envDefault:"0.1.0"`
		// Env "dev" allows an auto-generated signing key and non-secure cookies.
		Env string `env:"NXL_ENV" envDefault:"dev"`
	}

	// HTTP -.
	HTTP struct {
		Port string `env:"IDENTITY_HTTP_PORT" envDefault:"8090"`
		// Internal API (account snapshots for other services); reachable only inside the Docker network.
		InternalPort   string `env:"IDENTITY_INTERNAL_PORT" envDefault:"8089"`
		InternalSecret string `env:"NXL_INTERNAL_SECRET"`
	}

	// Log -.
	Log struct {
		Level string `env:"IDENTITY_LOG_LEVEL" envDefault:"debug"`
	}

	// PG -.
	PG struct {
		PoolMax int    `env:"IDENTITY_PG_POOL_MAX" envDefault:"4"`
		URL     string `env:"NXL_PG_URL_IDENTITY,required"`
	}

	// Auth -.
	Auth struct {
		// Base64 of a 32-byte Ed25519 seed; required outside dev.
		JWTPrivateKey string        `env:"IDENTITY_JWT_PRIVATE_KEY"`
		JWTIssuer     string        `env:"IDENTITY_JWT_ISSUER" envDefault:"nexuslive-identity"`
		AccessTTL     time.Duration `env:"IDENTITY_ACCESS_TTL" envDefault:"15m"`
		RefreshTTL    time.Duration `env:"IDENTITY_REFRESH_TTL" envDefault:"720h"`
	}

	// Cookies -.
	Cookies struct {
		Secure bool `env:"IDENTITY_COOKIE_SECURE" envDefault:"false"`
	}
)

// ErrInternalSecretRequired -.
var ErrInternalSecretRequired = errors.New("NXL_INTERNAL_SECRET is required outside dev")

// _devInternalSecret keeps `make nxl-up` working without an .env; refused outside dev.
const _devInternalSecret = "dev-internal-secret" //nolint:gosec // dev-only default, refused outside dev

// IsDev reports whether the service runs in local development.
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
