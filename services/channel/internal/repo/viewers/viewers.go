// Package viewers reads the live viewer counts that chat-svc writes to Redis (key viewers:{streamId}).
package viewers

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	goredis "github.com/redis/go-redis/v9"
)

// _keyPrefix is shared with chat-svc, which owns the keys.
const _keyPrefix = "viewers:"

// Repo implements repo.ViewerCountRepo.
type Repo struct {
	rdb *redis.Client
}

var _ repo.ViewerCountRepo = (*Repo)(nil)

// New -.
func New(rdb *redis.Client) *Repo {
	return &Repo{rdb: rdb}
}

// ViewerCounts reads every key in one MGET.
func (r *Repo) ViewerCounts(ctx context.Context, streamIDs []string) (map[string]int, error) {
	out := make(map[string]int, len(streamIDs))
	if len(streamIDs) == 0 {
		return out, nil
	}

	keys := make([]string, len(streamIDs))
	for i, id := range streamIDs {
		keys[i] = _keyPrefix + id
	}

	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return nil, fmt.Errorf("viewers - ViewerCounts: %w", err)
	}

	for i, v := range vals {
		s, ok := v.(string)
		if !ok {
			continue // missing key
		}

		if n, perr := strconv.Atoi(s); perr == nil && n >= 0 {
			out[streamIDs[i]] = n
		}
	}

	return out, nil
}
