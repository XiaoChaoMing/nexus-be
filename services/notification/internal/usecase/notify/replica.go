package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
)

// PatchUser -.
func (uc *UseCase) PatchUser(ctx context.Context, at time.Time, u entity.UserCopy) error {
	if err := uc.r.Replica.PatchUser(ctx, at, u); err != nil {
		return fmt.Errorf("replica - PatchUser: %w", err)
	}

	return nil
}

// PatchChannel -.
func (uc *UseCase) PatchChannel(ctx context.Context, at time.Time, c entity.ChannelPatch) error {
	if err := uc.r.Replica.PatchChannel(ctx, at, c); err != nil {
		return fmt.Errorf("replica - PatchChannel: %w", err)
	}

	return nil
}

// SetFollow -.
func (uc *UseCase) SetFollow(ctx context.Context, at time.Time, f entity.Follow) error {
	if err := uc.r.Replica.SetFollow(ctx, at, f); err != nil {
		return fmt.Errorf("replica - SetFollow: %w", err)
	}

	return nil
}

// RemoveFollow -.
func (uc *UseCase) RemoveFollow(ctx context.Context, at time.Time, userID, channelID string) error {
	if err := uc.r.Replica.RemoveFollow(ctx, at, userID, channelID); err != nil {
		return fmt.Errorf("replica - RemoveFollow: %w", err)
	}

	return nil
}

// SetBlock -.
func (uc *UseCase) SetBlock(ctx context.Context, blockerID, blockedID string, blocked bool) error {
	if err := uc.r.Replica.SetBlock(ctx, blockerID, blockedID, blocked); err != nil {
		return fmt.Errorf("replica - SetBlock: %w", err)
	}

	return nil
}

// Backfill copies the identity and channel-svc snapshots once (JetStream keeps 7 days only); rows carry
// the source's updated_at, so events replayed afterwards only overwrite them when newer.
func (uc *UseCase) Backfill(ctx context.Context) error {
	steps := []struct {
		name string
		run  func(ctx context.Context) (int, error)
	}{
		{"users", func(ctx context.Context) (int, error) {
			return backfill(ctx, uc.r.Snapshot.Users, func(ctx context.Context, s repo.Snapshot[entity.UserCopy]) error {
				return uc.r.Replica.PatchUser(ctx, s.UpdatedAt, s.Row)
			})
		}},
		{"user_blocks", func(ctx context.Context) (int, error) {
			return backfill(ctx, uc.r.Snapshot.Blocks, func(ctx context.Context, b [2]string) error {
				return uc.r.Replica.SetBlock(ctx, b[0], b[1], true)
			})
		}},
		{"channels", func(ctx context.Context) (int, error) {
			return backfill(ctx, uc.r.Snapshot.Channels, func(ctx context.Context, s repo.Snapshot[entity.ChannelPatch]) error {
				return uc.r.Replica.PatchChannel(ctx, s.UpdatedAt, s.Row)
			})
		}},
		{"follows", func(ctx context.Context) (int, error) {
			return backfill(ctx, uc.r.Snapshot.Follows, func(ctx context.Context, f entity.Follow) error {
				// The follow snapshot has no updated_at: rows are stamped with the time of the backfill.
				return uc.r.Replica.SetFollow(ctx, uc.now(), f)
			})
		}},
	}

	for _, s := range steps {
		done, err := uc.r.Replica.BackfillDone(ctx, s.name)
		if err != nil {
			return fmt.Errorf("replica - Backfill %s: %w", s.name, err)
		}

		if done {
			continue
		}

		n, err := s.run(ctx)
		if err != nil {
			return fmt.Errorf("replica - Backfill %s: %w", s.name, err)
		}

		if err := uc.r.Replica.MarkBackfilled(ctx, s.name); err != nil {
			return fmt.Errorf("replica - Backfill %s: %w", s.name, err)
		}

		uc.l.Info("notification - backfilled %d %s", n, s.name)
	}

	return nil
}

func backfill[T any](ctx context.Context, fetch func(ctx context.Context, after string, limit int) ([]T, string, error),
	put func(ctx context.Context, row T) error,
) (int, error) {
	after, n := "", 0

	for {
		rows, next, err := fetch(ctx, after, _backfillPage)
		if err != nil {
			return n, err
		}

		for _, r := range rows {
			if err := put(ctx, r); err != nil {
				return n, err
			}
		}

		n += len(rows)
		if next == "" || len(rows) == 0 {
			return n, nil
		}

		after = next
	}
}
