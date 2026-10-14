// Package config reads chat-svc settings from the environment.
package config

import (
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
		Redis   Redis
		Auth    Auth
		WS      WS
		Chat    Chat
		Swagger Swagger
	}

	// Swagger -.
	Swagger struct {
		Enabled bool `env:"NXL_SWAGGER_ENABLED" envDefault:"true"`
	}

	// App -.
	App struct {
		Name string `env:"CHAT_APP_NAME" envDefault:"chat-svc"`
		Env  string `env:"NXL_ENV"       envDefault:"dev"`
	}

	// HTTP serves the WebSocket gateway and the REST API on one port.
	HTTP struct {
		Port string `env:"CHAT_HTTP_PORT" envDefault:"8095"`
	}

	// Log -.
	Log struct {
		Level string `env:"CHAT_LOG_LEVEL" envDefault:"debug"`
	}

	// PG -.
	PG struct {
		PoolMax int    `env:"CHAT_PG_POOL_MAX"         envDefault:"8"`
		URL     string `env:"NXL_PG_URL_CHAT,required"`
	}

	// NATS carries domain events (JetStream) and the realtime fan-out between instances (core NATS).
	NATS struct {
		URL string `env:"CHAT_NATS_URL" envDefault:"nats://guest:guest@localhost:4222/"`
	}

	// Redis holds sequence numbers, resume buffers, rate limits, follow/sub copies and viewer counts.
	Redis struct {
		Addr string `env:"CHAT_REDIS_ADDR" envDefault:"localhost:6379"`
	}

	// Auth -.
	Auth struct {
		JWKSURL   string `env:"CHAT_JWKS_URL"       envDefault:"http://localhost:8090/v1/auth/.well-known/jwks.json"`
		JWTIssuer string `env:"IDENTITY_JWT_ISSUER" envDefault:"nexuslive-identity"`
	}

	// WS are the realtime gateway limits (NXL-00 §10, §12).
	WS struct {
		AuthTimeout      time.Duration `env:"CHAT_WS_AUTH_TIMEOUT"      envDefault:"5s"`
		Heartbeat        time.Duration `env:"CHAT_WS_HEARTBEAT"         envDefault:"25s"`
		IdleTimeout      time.Duration `env:"CHAT_WS_IDLE_TIMEOUT"      envDefault:"60s"`
		MaxSubscriptions int           `env:"CHAT_WS_MAX_SUBSCRIPTIONS" envDefault:"20"`
		MaxConnPerUser   int           `env:"CHAT_WS_MAX_CONN_PER_USER" envDefault:"5"`
		ResumeBuffer     int           `env:"CHAT_WS_RESUME_BUFFER"     envDefault:"200"`
		ResumeTTL        time.Duration `env:"CHAT_WS_RESUME_TTL"        envDefault:"5m"`
		SendQueue        int           `env:"CHAT_WS_SEND_QUEUE"        envDefault:"256"`
		// Browser origins allowed to open /v1/ws. Clients that send no Origin (k6, scripts) are allowed.
		AllowedOrigins []string `env:"CHAT_ALLOWED_ORIGINS" envDefault:"http://localhost:5173" envSeparator:","`
	}

	// Chat holds the NXL-B values (TSD §9).
	Chat struct {
		RateLimit           int           `env:"CHAT_RATE_LIMIT"             envDefault:"20"`
		RateWindow          time.Duration `env:"CHAT_RATE_WINDOW"            envDefault:"30s"`
		RateLimitPrivileged int           `env:"CHAT_RATE_LIMIT_PRIVILEGED"  envDefault:"100"`
		RateLimitIP         int           `env:"CHAT_RATE_LIMIT_IP"          envDefault:"60"`
		DuplicateWindow     time.Duration `env:"CHAT_DUPLICATE_WINDOW"       envDefault:"30s"`
		MaxLines            int           `env:"CHAT_MAX_LINES"              envDefault:"3"`
		HistoryOnJoin       int           `env:"CHAT_HISTORY_ON_JOIN"        envDefault:"50"`
		SamplingThreshold   int           `env:"CHAT_SAMPLING_THRESHOLD"     envDefault:"30"`
		SamplingCap         int           `env:"CHAT_SAMPLING_CAP"           envDefault:"15"`
		Retention           time.Duration `env:"CHAT_RETENTION"              envDefault:"720h"`
		Disabled            bool          `env:"CHAT_DISABLED"               envDefault:"false"`
		RequireVerified     bool          `env:"CHAT_REQUIRE_VERIFIED_EMAIL" envDefault:"false"`
		ViewerInterval      time.Duration `env:"CHAT_VIEWER_INTERVAL"        envDefault:"10s"`
		PartitionTick       time.Duration `env:"CHAT_PARTITION_TICK"         envDefault:"1h"`
	}
)

// NewConfig returns app config.
func NewConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	return cfg, nil
}
