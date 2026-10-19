// Package redisstore keeps billing's short-lived state in Redis: job locks, the sync rate limit and the
// fake PSP order states.
package redisstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/billing/internal/repo"
	goredis "github.com/redis/go-redis/v9"
)

const (
	_jobPrefix     = "billing:job:"
	_syncPrefix    = "billing:sync:"
	_fakePrefix    = "billing:fakepsp:"
	_syncWindow    = time.Minute
	_fakeTTL       = 7 * 24 * time.Hour
	_releaseScript = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0`
	_scanCount     = 200
)

// Store implements repo.JobLock, repo.SyncLimiter and fake.Store.
type Store struct {
	rdb      goredis.UniversalClient
	instance string
	release  *goredis.Script
}

var (
	_ repo.JobLock     = (*Store)(nil)
	_ repo.SyncLimiter = (*Store)(nil)
)

// New -.
func New(rdb goredis.UniversalClient) *Store {
	return &Store{rdb: rdb, instance: uuid.NewString(), release: goredis.NewScript(_releaseScript)}
}

// TryLock takes billing:job:{name} for ttl; release deletes it only while this instance still holds it.
func (s *Store) TryLock(ctx context.Context, name string, ttl time.Duration) (release func(), ok bool, err error) {
	key := _jobPrefix + name

	ok, err = s.rdb.SetNX(ctx, key, s.instance, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("redisstore - lock %s: %w", name, err)
	}

	if !ok {
		return nil, false, nil
	}

	release = func() {
		// A fresh context: the job's own context may already be canceled at shutdown.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()

		_ = s.release.Run(rctx, s.rdb, []string{key}, s.instance).Err() //nolint:errcheck // the lock expires anyway
	}

	return release, true, nil
}

// LastRun reads billing:job:{name}:last ("" when never run).
func (s *Store) LastRun(ctx context.Context, name string) (string, error) {
	v, err := s.rdb.Get(ctx, _jobPrefix+name+":last").Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("redisstore - last run %s: %w", name, err)
	}

	return v, nil
}

// SetLastRun -.
func (s *Store) SetLastRun(ctx context.Context, name, value string) error {
	if err := s.rdb.Set(ctx, _jobPrefix+name+":last", value, 0).Err(); err != nil {
		return fmt.Errorf("redisstore - set last run %s: %w", name, err)
	}

	return nil
}

// Hit counts one sync call of the order in the current minute window.
func (s *Store) Hit(ctx context.Context, orderID string) (int, time.Duration, error) {
	key := _syncPrefix + orderID
	pipe := s.rdb.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, _syncWindow)
	ttl := pipe.PTTL(ctx, key)

	if _, err := pipe.Exec(ctx); err != nil {
		return 0, 0, fmt.Errorf("redisstore - sync hit: %w", err)
	}

	return int(incr.Val()), ttl.Val(), nil
}

// Get returns the fake order state, empty when unknown.
func (s *Store) Get(ctx context.Context, invoice string) (map[string]string, error) {
	m, err := s.rdb.HGetAll(ctx, _fakePrefix+invoice).Result()
	if err != nil {
		return nil, fmt.Errorf("redisstore - fake get: %w", err)
	}

	return m, nil
}

// Set replaces the fake order state.
func (s *Store) Set(ctx context.Context, invoice string, fields map[string]string) error {
	key := _fakePrefix + invoice
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, key)
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, _fakeTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redisstore - fake set: %w", err)
	}

	return nil
}

// Invoices lists the invoices with a fake state.
func (s *Store) Invoices(ctx context.Context) ([]string, error) {
	var out []string

	iter := s.rdb.Scan(ctx, 0, _fakePrefix+"*", _scanCount).Iterator()
	for iter.Next(ctx) {
		out = append(out, strings.TrimPrefix(iter.Val(), _fakePrefix))
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("redisstore - fake scan: %w", err)
	}

	return out, nil
}
