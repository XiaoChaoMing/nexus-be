// Package config reads notification-svc settings from the environment.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type (
	// Config is read from the environment only.
	Config struct {
		App          App
		HTTP         HTTP
		Log          Log
		PG           PG
		NATS         NATS
		Redis        Redis
		Auth         Auth
		Internal     Internal
		Mail         Mail
		Notification Notification
		Swagger      Swagger
	}

	// Swagger -.
	Swagger struct {
		Enabled bool `env:"NXL_SWAGGER_ENABLED" envDefault:"true"`
	}

	// App -.
	App struct {
		Name string `env:"NOTIFICATION_APP_NAME" envDefault:"notification-svc"`
		Env  string `env:"NXL_ENV"               envDefault:"dev"`
	}

	// HTTP -.
	HTTP struct {
		Port string `env:"NOTIFICATION_HTTP_PORT" envDefault:"8097"`
	}

	// Log -.
	Log struct {
		Level string `env:"NOTIFICATION_LOG_LEVEL" envDefault:"debug"`
	}

	// PG -.
	PG struct {
		PoolMax int    `env:"NOTIFICATION_PG_POOL_MAX"         envDefault:"8"`
		URL     string `env:"NXL_PG_URL_NOTIFICATION,required"`
	}

	// NATS -.
	NATS struct {
		URL string `env:"NATS_URL" envDefault:"nats://guest:guest@localhost:4222/"`
	}

	// Redis holds the live cooldowns and the daily e-mail counts (notify:*).
	Redis struct {
		Addr string `env:"NOTIFICATION_REDIS_ADDR" envDefault:"localhost:6379"`
	}

	// Auth -.
	Auth struct {
		JWKSURL   string `env:"NOTIFICATION_JWKS_URL" envDefault:"http://localhost:8090/v1/auth/.well-known/jwks.json"`
		JWTIssuer string `env:"IDENTITY_JWT_ISSUER"   envDefault:"nexuslive-identity"`
	}

	// Internal are the snapshot APIs of the one-off backfill of the copies.
	Internal struct {
		Secret      string `env:"NXL_INTERNAL_SECRET"`
		IdentityURL string `env:"NOTIFICATION_IDENTITY_INTERNAL_URL" envDefault:"http://localhost:8089"`
		ChannelURL  string `env:"NOTIFICATION_CHANNEL_INTERNAL_URL"  envDefault:"http://localhost:8088"`
	}

	// Mail -.
	Mail struct {
		// Empty disables sending: e-mails are logged and marked SENT.
		SMTPAddr string `env:"NOTIFICATION_SMTP_ADDR"`
		From     string `env:"NOTIFICATION_MAIL_FROM"     envDefault:"NexusLive <no-reply@nexuslive.dev>"`
		// Every link of an e-mail is WebBaseURL + path.
		WebBaseURL string `env:"NOTIFICATION_WEB_BASE_URL" envDefault:"http://localhost:5173"`
		// UnsubscribeSecret signs the unsubscribe links (≥ 32 characters; random in dev when empty).
		UnsubscribeSecret string        `env:"NOTIFICATION_UNSUBSCRIBE_SECRET"`
		Tick              time.Duration `env:"NOTIFICATION_EMAIL_TICK"         envDefault:"5s"`
		// AuthMailerEnabled consumes auth.email_requested.v1 (verification, reset, security lock); false
		// deletes the durable notification-auth-mailer (mailer cutover, TSD §9.2).
		AuthMailerEnabled bool `env:"NOTIFICATION_AUTH_MAILER_ENABLED" envDefault:"false"`
		// Shown in the account e-mails only; identity's IDENTITY_EMAIL_VERIFY_TTL, IDENTITY_PASSWORD_RESET_TTL
		// and IDENTITY_LOGIN_LOCK_DURATION.
		VerifyTTL    time.Duration `env:"NOTIFICATION_VERIFY_TTL"     envDefault:"24h"`
		ResetTTL     time.Duration `env:"NOTIFICATION_RESET_TTL"      envDefault:"30m"`
		LockDuration time.Duration `env:"NOTIFICATION_LOCK_DURATION"  envDefault:"15m"`
	}

	// Notification are the NXL-E values (NXL-00 §12).
	Notification struct {
		Retention         time.Duration `env:"NOTIFICATION_RETENTION"           envDefault:"2160h"`
		LiveCooldown      time.Duration `env:"NOTIFICATION_LIVE_COOLDOWN"       envDefault:"30m"`
		LiveEmailCooldown time.Duration `env:"NOTIFICATION_LIVE_EMAIL_COOLDOWN" envDefault:"12h"`
		EmailDailyMax     int           `env:"NOTIFICATION_EMAIL_DAILY_MAX"     envDefault:"10"`
	}
)

var (
	// ErrInternalSecretRequired -.
	ErrInternalSecretRequired = errors.New("NXL_INTERNAL_SECRET is required outside dev")
	// ErrUnsubscribeSecret -.
	ErrUnsubscribeSecret = errors.New("NOTIFICATION_UNSUBSCRIBE_SECRET of at least 32 characters is required outside dev")
)

const (
	// _devInternalSecret keeps `make nxl-up` working without an .env; refused outside dev.
	_devInternalSecret = "dev-internal-secret" //nolint:gosec // dev-only default, refused outside dev
	_minSecretLen      = 32
)

// NewConfig returns app config. GeneratedSecret reports a random dev unsubscribe secret (links break on
// restart).
func NewConfig() (cfg *Config, generatedSecret bool, err error) {
	cfg = &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, false, fmt.Errorf("config error: %w", err)
	}

	dev := cfg.App.Env == "dev"

	if cfg.Internal.Secret == "" {
		if !dev {
			return nil, false, fmt.Errorf("config error: %w", ErrInternalSecretRequired)
		}

		cfg.Internal.Secret = _devInternalSecret
	}

	switch {
	case len(cfg.Mail.UnsubscribeSecret) >= _minSecretLen, dev && cfg.Mail.UnsubscribeSecret != "":
	case !dev:
		return nil, false, fmt.Errorf("config error: %w", ErrUnsubscribeSecret)
	default:
		b := make([]byte, _minSecretLen)
		if _, err := rand.Read(b); err != nil {
			return nil, false, fmt.Errorf("config error: random secret: %w", err)
		}

		cfg.Mail.UnsubscribeSecret, generatedSecret = hex.EncodeToString(b), true
	}

	return cfg, generatedSecret, nil
}
