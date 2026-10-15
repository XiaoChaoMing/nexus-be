package realtime

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
)

// _lockedKey marks a locked account: auth with a token that is still valid is refused (close 4003).
const _lockedKey = "chat:locked:"

var _ repo.LockStore = (*Store)(nil)

// Lock -.
func (s *Store) Lock(ctx context.Context, userID string, until *time.Time) error {
	var ttl time.Duration

	if until != nil {
		ttl = time.Until(*until)
		if ttl <= 0 {
			return s.Unlock(ctx, userID)
		}
	}

	if err := s.rdb.Set(ctx, _lockedKey+userID, "1", ttl).Err(); err != nil {
		return fmt.Errorf("realtime - Lock: %w", err)
	}

	return nil
}

// Unlock -.
func (s *Store) Unlock(ctx context.Context, userID string) error {
	if err := s.rdb.Del(ctx, _lockedKey+userID).Err(); err != nil {
		return fmt.Errorf("realtime - Unlock: %w", err)
	}

	return nil
}

// Locked -.
func (s *Store) Locked(ctx context.Context, userID string) (bool, error) {
	n, err := s.rdb.Exists(ctx, _lockedKey+userID).Result()
	if err != nil {
		return false, fmt.Errorf("realtime - Locked: %w", err)
	}

	return n > 0, nil
}
