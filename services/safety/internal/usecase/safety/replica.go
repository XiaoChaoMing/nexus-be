package safety

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
)

// PatchUser -.
func (uc *UseCase) PatchUser(ctx context.Context, eventID string, at time.Time, p repo.UserPatch) error {
	if err := uc.r.Replica.PatchUser(ctx, eventID, at, p); err != nil {
		return fmt.Errorf("replica - PatchUser: %w", err)
	}

	return nil
}

// PatchChannel -.
func (uc *UseCase) PatchChannel(ctx context.Context, eventID string, at time.Time, p repo.ChannelPatch) error {
	if err := uc.r.Replica.PatchChannel(ctx, eventID, at, p); err != nil {
		return fmt.Errorf("replica - PatchChannel: %w", err)
	}

	return nil
}

// PatchStream -.
func (uc *UseCase) PatchStream(ctx context.Context, eventID string, at time.Time, p repo.StreamPatch) error {
	if err := uc.r.Replica.PatchStream(ctx, eventID, at, p); err != nil {
		return fmt.Errorf("replica - PatchStream: %w", err)
	}

	return nil
}

// Backfill copies the identity / channel / stream snapshots once (§6.10); rows are stamped with the
// source's updated_at, so events replayed afterwards only overwrite them when they are newer.
func (uc *UseCase) Backfill(ctx context.Context) error {
	steps := []struct {
		name string
		run  func(ctx context.Context) (int, error)
	}{
		{"users", func(ctx context.Context) (int, error) {
			return backfill(ctx, uc.r.Snapshot.Users, uc.r.Replica.PatchUser)
		}},
		{"channels", func(ctx context.Context) (int, error) {
			return backfill(ctx, uc.r.Snapshot.Channels, uc.r.Replica.PatchChannel)
		}},
		{"streams", func(ctx context.Context) (int, error) {
			return backfill(ctx, uc.r.Snapshot.Streams, uc.r.Replica.PatchStream)
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

		uc.l.Info("safety - backfilled %d %s", n, s.name)
	}

	return nil
}

func backfill[T any](ctx context.Context,
	fetch func(ctx context.Context, after string, limit int) ([]repo.Snapshot[T], string, error),
	patch func(ctx context.Context, eventID string, at time.Time, p T) error,
) (int, error) {
	after, n := "", 0

	for {
		rows, next, err := fetch(ctx, after, _backfillPage)
		if err != nil {
			return n, err
		}

		for _, r := range rows {
			if err := patch(ctx, "", r.UpdatedAt, r.Row); err != nil {
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
