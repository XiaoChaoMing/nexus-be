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
		S3      S3
		Upload  Upload
		Swagger Swagger
	}

	// S3 is the object storage of upload sources (MinIO); not used while uploads are turned off.
	S3 struct {
		Endpoint  string `env:"STREAM_S3_ENDPOINT"   envDefault:"minio:9000"`
		AccessKey string `env:"STREAM_S3_ACCESS_KEY"`
		SecretKey string `env:"STREAM_S3_SECRET_KEY"`
		UseSSL    bool   `env:"STREAM_S3_USE_SSL"    envDefault:"false"`
	}

	// Upload configures video uploads (Studio): parts are written to S3 multipart uploads in Bucket.
	Upload struct {
		// Enabled false answers 503 STORAGE_UNAVAILABLE on /v1/studio/uploads and needs no S3.
		Enabled bool   `env:"STREAM_UPLOAD_ENABLED" envDefault:"true"`
		Bucket  string `env:"STREAM_UPLOAD_BUCKET"  envDefault:"nxl-uploads"`
		// MaxBytes is the largest file (2 GiB); PartSize the size of every part but the last (8 MiB, at least 5 MiB
		// for S3 and at most BodyLimit).
		MaxBytes int64 `env:"STREAM_UPLOAD_MAX_BYTES" envDefault:"2147483648"`
		PartSize int64 `env:"STREAM_UPLOAD_PART_SIZE" envDefault:"8388608"`
		// TTL is how long a session takes parts.
		TTL time.Duration `env:"STREAM_UPLOAD_TTL" envDefault:"24h"`
		// MaxActive limits a channel's sessions uploading plus videos processing; DailyMax its sessions per 24 h.
		MaxActive int `env:"STREAM_UPLOAD_MAX_ACTIVE" envDefault:"3"`
		DailyMax  int `env:"STREAM_UPLOAD_DAILY_MAX"  envDefault:"10"`
		// ExpiryTick runs the job that expires stale sessions and deletes transcoded sources.
		ExpiryTick time.Duration `env:"STREAM_UPLOAD_EXPIRY_TICK" envDefault:"10m"`
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
		// HLSSigning makes playback URLs /hls/t/{expires}/{md5}/streams/{id}/… (nginx secure_link) and empties the
		// manifestUrl of the public DTOs. HLSSecret is NXL_HLS_SECRET of the edge, at least 32 characters.
		HLSSigning bool   `env:"STREAM_HLS_SIGNING" envDefault:"false"`
		HLSSecret  string `env:"STREAM_HLS_SECRET"`
		// NXL-A §7A: how long an interrupted stream waits for OBS before it ends.
		GracePeriod  time.Duration `env:"STREAM_INGEST_GRACE_PERIOD" envDefault:"60s"`
		VODRetention time.Duration `env:"STREAM_VOD_RETENTION" envDefault:"336h"`
		ExpiryTick   time.Duration `env:"STREAM_EXPIRY_TICK" envDefault:"5s"`
		// After the creator ends a stream, publishes are refused this long so OBS auto-reconnect
		// does not start a new broadcast. 0 disables it.
		EndCooldown   time.Duration `env:"STREAM_END_COOLDOWN" envDefault:"30s"`
		VODExpiryTick time.Duration `env:"STREAM_VOD_EXPIRY_TICK" envDefault:"10m"`
		// Go Live attaches to a SCHEDULED stream from ScheduleEarlyStart before its start until ScheduleLateGrace
		// after it; a SCHEDULED stream not started by then is CANCELLED (checked every ScheduleTick, 0 = off).
		ScheduleEarlyStart time.Duration `env:"STREAM_SCHEDULE_EARLY_START" envDefault:"30m"`
		ScheduleLateGrace  time.Duration `env:"STREAM_SCHEDULE_LATE_GRACE" envDefault:"60m"`
		ScheduleTick       time.Duration `env:"STREAM_SCHEDULE_TICK" envDefault:"1m"`
	}
)

// ErrInternalSecretRequired -.
var ErrInternalSecretRequired = errors.New("NXL_INTERNAL_SECRET is required outside dev")

// ErrHLSSecretTooShort -.
var ErrHLSSecretTooShort = errors.New("STREAM_HLS_SIGNING needs STREAM_HLS_SECRET of at least 32 characters")

// ErrS3CredentialsRequired -.
var ErrS3CredentialsRequired = errors.New("STREAM_UPLOAD_ENABLED needs STREAM_S3_ACCESS_KEY and STREAM_S3_SECRET_KEY")

// ErrUploadLimits -.
var ErrUploadLimits = errors.New("STREAM_UPLOAD_* out of range")

// BodyLimit is the largest request body of the public API: one upload part (8 MiB) plus headroom.
const BodyLimit = 9 << 20

const (
	_minHLSSecret = 32
	// S3 refuses parts below 5 MiB (except the last) and uploads of more than 10 000 parts.
	_minPartSize = 5 << 20
	_maxParts    = 10000
)

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

	if cfg.Stream.HLSSigning && len(cfg.Stream.HLSSecret) < _minHLSSecret {
		return nil, fmt.Errorf("config error: %w", ErrHLSSecretTooShort)
	}

	if err := cfg.Upload.validate(cfg.S3); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	return cfg, nil
}

func (u Upload) validate(s3 S3) error {
	if !u.Enabled {
		return nil
	}

	if s3.AccessKey == "" || s3.SecretKey == "" {
		return ErrS3CredentialsRequired
	}

	return u.validateLimits()
}

func (u Upload) validateLimits() error {
	if u.PartSize < _minPartSize || u.PartSize > BodyLimit || u.MaxBytes <= 0 ||
		(u.MaxBytes+u.PartSize-1)/u.PartSize > _maxParts {
		return fmt.Errorf("%w: part size %d, max bytes %d", ErrUploadLimits, u.PartSize, u.MaxBytes)
	}

	if u.TTL <= 0 || u.ExpiryTick <= 0 || u.MaxActive < 1 || u.DailyMax < 1 || u.Bucket == "" {
		return fmt.Errorf("%w: ttl, expiry tick, max active, daily max and bucket must be set", ErrUploadLimits)
	}

	return nil
}
