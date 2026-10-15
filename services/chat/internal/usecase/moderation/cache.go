package moderation

import (
	"sync"
	"time"
)

// ttlCache keeps values for a short time (ROLE_CACHE_TTL, AUTOMOD_CACHE_TTL). There is no sweeper: an expired
// entry is replaced when it is read again, and the key space (channels, channel × user) stays small.
type ttlCache struct {
	ttl time.Duration
	m   sync.Map
}

type cached struct {
	val any
	exp time.Time
}

func (c *ttlCache) get(key string, now time.Time) (any, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return nil, false
	}

	e, ok := v.(cached)
	if !ok || now.After(e.exp) {
		return nil, false
	}

	return e.val, true
}

func (c *ttlCache) set(key string, val any, now time.Time) {
	c.m.Store(key, cached{val: val, exp: now.Add(c.ttl)})
}

func (c *ttlCache) drop(key string) {
	c.m.Delete(key)
}
