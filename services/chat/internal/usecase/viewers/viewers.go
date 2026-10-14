// Package viewers counts the people watching each stream (TSD S4, owner decision G3-Q6): one viewer
// key per account ("u:"), guest browser ("a:") or, without either, connection ("c:"), however many
// tabs, connections or instances it uses. Every instance refreshes its keys in a Redis ZSET per stream;
// the instance holding a short lease drops stale keys, stores viewers:{streamId} for stream-svc and
// publishes viewer_count when the number changes.
package viewers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// Derived from the tick interval (10 s → stale after 25 s, count kept 30 s, lease 15 s).
const (
	_staleTicks = 2.5
	_countTicks = 3
	_leaseTicks = 1.5
)

// Publisher is the realtime fan-out (fanout.Publisher).
type Publisher interface {
	Publish(ctx context.Context, topic entity.Topic, typ string, data any) (entity.Event, error)
}

// UseCase implements usecase.Viewers.
type UseCase struct {
	store    repo.PresenceStore
	pub      Publisher
	instance string
	interval time.Duration
	l        logger.Interface
	now      func() time.Time

	mu sync.Mutex
	// local counts, per stream, the subscriptions of each viewer key on this instance.
	local map[string]map[string]int
}

var _ usecase.Viewers = (*UseCase)(nil)

// New -.
func New(store repo.PresenceStore, pub Publisher, instance string, interval time.Duration, l logger.Interface) *UseCase {
	return &UseCase{
		store: store, pub: pub, instance: instance, interval: interval, l: l, now: time.Now,
		local: map[string]map[string]int{},
	}
}

// WithClock replaces the clock (tests).
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

// stale is how long a key counts without a refresh.
func (uc *UseCase) stale() time.Duration { return time.Duration(float64(uc.interval) * _staleTicks) }

// Join -.
func (uc *UseCase) Join(ctx context.Context, streamID, key string) (int, error) {
	uc.mu.Lock()

	keys, ok := uc.local[streamID]
	if !ok {
		keys = map[string]int{}
		uc.local[streamID] = keys
	}

	keys[key]++
	uc.mu.Unlock()

	now := uc.now()
	if err := uc.store.TouchViewers(ctx, streamID, []string{key}, now); err != nil {
		return 0, fmt.Errorf("ViewersUseCase - Join: %w", err)
	}

	n, err := uc.store.CountViewers(ctx, streamID, now.Add(-uc.stale()))
	if err != nil {
		return 0, fmt.Errorf("ViewersUseCase - Join - count: %w", err)
	}

	return n, nil
}

// Leave stops refreshing the key once its last subscription on this instance is gone; the key then
// expires from the ZSET (another tab on another instance keeps refreshing it meanwhile).
func (uc *UseCase) Leave(streamID, key string) {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	keys := uc.local[streamID]
	if keys == nil {
		return
	}

	keys[key]--
	if keys[key] <= 0 {
		delete(keys, key)
	}

	if len(keys) == 0 {
		delete(uc.local, streamID)
	}
}

// Tick refreshes this instance's keys and, when it holds the lease, publishes the counts.
func (uc *UseCase) Tick(ctx context.Context) error {
	now := uc.now()

	for streamID, keys := range uc.snapshot() {
		if err := uc.store.TouchViewers(ctx, streamID, keys, now); err != nil {
			return fmt.Errorf("ViewersUseCase - Tick - touch: %w", err)
		}
	}

	lead, err := uc.store.LeadViewers(ctx, uc.instance, time.Duration(float64(uc.interval)*_leaseTicks))
	if err != nil {
		return fmt.Errorf("ViewersUseCase - Tick - lease: %w", err)
	}

	if !lead {
		return nil
	}

	streams, err := uc.store.WatchedStreams(ctx)
	if err != nil {
		return fmt.Errorf("ViewersUseCase - Tick - streams: %w", err)
	}

	for _, streamID := range streams {
		uc.publishCount(ctx, streamID, now)
	}

	return nil
}

func (uc *UseCase) publishCount(ctx context.Context, streamID string, now time.Time) {
	n, changed, err := uc.store.RefreshCount(ctx, streamID, now.Add(-uc.stale()), uc.interval*_countTicks)
	if err != nil {
		uc.l.Warn("viewers - refresh %s: %v", streamID, err)

		return
	}

	if changed {
		if _, err = uc.pub.Publish(ctx, entity.StreamTopic(streamID), entity.EventViewerCount, countPayload{Count: n}); err != nil {
			uc.l.Warn("viewers - publish %s: %v", streamID, err)
		}
	}

	if n == 0 {
		if err = uc.store.ForgetStream(ctx, streamID); err != nil {
			uc.l.Warn("viewers - forget %s: %v", streamID, err)
		}
	}
}

type countPayload struct {
	Count int `json:"count"`
}

func (uc *UseCase) snapshot() map[string][]string {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	out := make(map[string][]string, len(uc.local))

	for streamID, keys := range uc.local {
		list := make([]string, 0, len(keys))
		for k := range keys {
			list = append(list, k)
		}

		out[streamID] = list
	}

	return out
}
