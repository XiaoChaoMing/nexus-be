package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type (
	// Config is read from the environment only.
	Config struct {
		App       App
		Log       Log
		RTMP      RTMP
		HTTP      HTTP
		Media     Media
		Upload    Upload
		StreamSvc StreamSvc
		S3        S3
	}

	// App -.
	App struct {
		Name string `env:"MEDIA_APP_NAME" envDefault:"media-svc"`
		Env  string `env:"NXL_ENV" envDefault:"dev"`
	}

	// Log -.
	Log struct {
		Level string `env:"MEDIA_LOG_LEVEL" envDefault:"info"`
	}

	// RTMP -.
	RTMP struct {
		Addr string `env:"MEDIA_RTMP_ADDR" envDefault:":1935"`
		// Public address shown to creators, e.g. rtmp://localhost:1935/live.
		PublicURL string `env:"MEDIA_RTMP_PUBLIC_URL" envDefault:"rtmp://localhost:1935/live"`
	}

	// HTTP -.
	HTTP struct {
		Port string `env:"MEDIA_HTTP_PORT" envDefault:"8091"`
		// Serves /dev/player and proxies /hls to MinIO; for local testing only.
		DevPlayer bool `env:"MEDIA_DEV_PLAYER" envDefault:"true"`
	}

	// Media -.
	Media struct {
		WorkDir              string `env:"MEDIA_WORK_DIR" envDefault:"/var/lib/media"`
		FFmpegPath           string `env:"MEDIA_FFMPEG_PATH" envDefault:"ffmpeg"`
		MaxConcurrentStreams int    `env:"MEDIA_MAX_CONCURRENT_STREAMS" envDefault:"2"`
		// How long an interrupted stream's files wait for a reconnect. With stream-svc this is only a
		// safety net: stream-svc ends the stream after its own 60 s and media-svc finalizes on stream.ended.
		GracePeriod time.Duration `env:"MEDIA_GRACE_PERIOD" envDefault:"90s"`
		// Standalone mode (no stream-svc): the single stream key allowed to publish.
		StaticStreamKey string        `env:"MEDIA_STATIC_STREAM_KEY"`
		Preset          string        `env:"MEDIA_X264_PRESET" envDefault:"veryfast"`
		HealthInterval  time.Duration `env:"MEDIA_HEALTH_INTERVAL" envDefault:"5s"`
	}

	// Upload transcodes uploaded videos (upload.completed.v1) into HLS under videos/{vodId}/.
	Upload struct {
		// Jobs run at once; 0 = upload.completed.v1 is not consumed at all. Live streams never count them.
		MaxTranscodes int           `env:"MEDIA_MAX_UPLOAD_TRANSCODES" envDefault:"1"`
		Preset        string        `env:"MEDIA_UPLOAD_X264_PRESET" envDefault:"veryfast"`
		MaxDuration   time.Duration `env:"MEDIA_UPLOAD_MAX_DURATION" envDefault:"2h"`
		// The only bucket source objects are read from (created by stream-svc).
		Bucket      string `env:"MEDIA_UPLOAD_BUCKET" envDefault:"nxl-uploads"`
		SegmentSec  int    `env:"MEDIA_UPLOAD_SEGMENT_SEC" envDefault:"4"`
		FFprobePath string `env:"MEDIA_FFPROBE_PATH" envDefault:"ffprobe"`
	}

	// StreamSvc -.
	StreamSvc struct {
		// Set → keys are checked by stream-svc and events go to NATS; empty → standalone (static key).
		URL            string `env:"MEDIA_STREAM_SVC_URL"`
		InternalSecret string `env:"NXL_INTERNAL_SECRET" envDefault:"dev-internal-secret"`
		NATSURL        string `env:"NATS_URL" envDefault:"nats://guest:guest@localhost:4222/"`
	}

	// S3 -.
	S3 struct {
		Endpoint     string `env:"MEDIA_S3_ENDPOINT" envDefault:"minio:9000"`
		AccessKey    string `env:"MEDIA_S3_ACCESS_KEY,required"`
		SecretKey    string `env:"MEDIA_S3_SECRET_KEY,required"`
		Bucket       string `env:"MEDIA_S3_BUCKET" envDefault:"nxl-media"`
		UseSSL       bool   `env:"MEDIA_S3_USE_SSL" envDefault:"false"`
		EnsureBucket bool   `env:"MEDIA_S3_ENSURE_BUCKET" envDefault:"true"`
		// Days before stream objects expire (PRD Q4).
		RetentionDays int `env:"MEDIA_S3_RETENTION_DAYS" envDefault:"14"`
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
