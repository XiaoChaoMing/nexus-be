// Package redis builds the Redis client shared by services (chat state, rate limits, viewer counts).
package redis

import (
	"context"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	_defaultPoolSize     = 20
	_defaultDialTimeout  = 2 * time.Second
	_defaultReadTimeout  = time.Second
	_defaultWriteTimeout = time.Second
	_retryWait           = time.Second
)

// Client wraps go-redis; services use its command methods directly.
type Client struct {
	*goredis.Client
}

// Option -.
type Option func(*goredis.Options)

// PoolSize sets the maximum number of connections.
func PoolSize(n int) Option {
	return func(o *goredis.Options) { o.PoolSize = n }
}

// New connects to addr ("host:port" or a redis:// URL) and pings it, retrying until ctx expires
// (Redis may start after the service under compose).
func New(ctx context.Context, addr string, opts ...Option) (*Client, error) {
	o, err := options(addr)
	if err != nil {
		return nil, err
	}

	for _, opt := range opts {
		opt(o)
	}

	c := &Client{goredis.NewClient(o)}

	for {
		perr := c.Healthy(ctx)
		if perr == nil {
			return c, nil
		}

		select {
		case <-ctx.Done():
			_ = c.Close() // giving up: the ping error is what matters

			return nil, fmt.Errorf("redis - New: %w (last error: %w)", ctx.Err(), perr)
		case <-time.After(_retryWait):
		}
	}
}

// Healthy pings the server.
func (c *Client) Healthy(ctx context.Context) error {
	if err := c.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis - ping: %w", err)
	}

	return nil
}

func options(addr string) (*goredis.Options, error) {
	if strings.Contains(addr, "://") {
		o, err := goredis.ParseURL(addr)
		if err != nil {
			return nil, fmt.Errorf("redis - parse url: %w", err)
		}

		return o, nil
	}

	return &goredis.Options{
		Addr:         addr,
		PoolSize:     _defaultPoolSize,
		DialTimeout:  _defaultDialTimeout,
		ReadTimeout:  _defaultReadTimeout,
		WriteTimeout: _defaultWriteTimeout,
	}, nil
}
