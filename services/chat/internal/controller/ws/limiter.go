package ws

import (
	"sync"
	"time"
)

// bucket is an in-memory token bucket: rate tokens per second, at most burst.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	at     time.Time
}

func newBucket(rate, burst float64) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst}
}

func (b *bucket) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.at.IsZero() {
		b.tokens = min(b.burst, b.tokens+now.Sub(b.at).Seconds()*b.rate)
	}

	b.at = now

	if b.tokens < 1 {
		return false
	}

	b.tokens--

	return true
}
