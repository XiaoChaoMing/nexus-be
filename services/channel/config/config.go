package config

import (
	"fmt"

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
		Swagger Swagger
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
)

// NewConfig returns app config.
func NewConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	return cfg, nil
}
