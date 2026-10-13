package persistent

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
)

// ChannelRepo -.
type ChannelRepo struct {
	*postgres.Postgres
}

// NewChannelRepo -.
func NewChannelRepo(pg *postgres.Postgres) repo.ChannelRepo {
	return &ChannelRepo{pg}
}

// UpsertChannel records the event id in processed_events in the same transaction, so a redelivery is a no-op.
func (r *ChannelRepo) UpsertChannel(ctx context.Context, eventID string, ch entity.UserChannel) error {
	return inTx(ctx, r.Pool, "ChannelRepo - UpsertChannel", func(tx pgx.Tx) error {
		if eventID != "" {
			tag, err := tx.Exec(ctx,
				`INSERT INTO processed_events (event_id, consumer) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				eventID, _consumer)
			if err != nil {
				return fmt.Errorf("ChannelRepo - UpsertChannel - processed_events: %w", err)
			}

			if tag.RowsAffected() == 0 {
				return nil
			}
		}

		_, err := tx.Exec(ctx,
			`INSERT INTO user_channels (user_id, channel_id, handle, display_name) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (user_id) DO UPDATE
			 SET channel_id = EXCLUDED.channel_id, handle = EXCLUDED.handle, display_name = EXCLUDED.display_name`,
			ch.UserID, ch.ChannelID, ch.Handle, ch.DisplayName)
		switch code, _ := pgErrorCode(err); code {
		case _foreignKeyViolation:
			return entity.ErrUserNotFound
		case _uniqueViolation:
			return entity.ErrChannelTaken
		}

		if err != nil {
			return fmt.Errorf("ChannelRepo - UpsertChannel - upsert: %w", err)
		}

		return nil
	})
}
