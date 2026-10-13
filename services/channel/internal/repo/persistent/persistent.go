// Package persistent implements channel-svc storage on PostgreSQL (database nxl_channel).
package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

const (
	_consumer = "channel-svc"
	// Upper bound of suffixed handle candidates tried for one channel (minhnn2 … minhnn1000).
	_maxHandleCandidates = 1000
)

const _channelCols = `id::text, owner_id::text, handle::text, display_name, bio, avatar_url, status,
	is_live, current_stream_id::text, created_at`

// Repo implements repo.ChannelRepo.
type Repo struct {
	*postgres.Postgres
}

// New -.
func New(pg *postgres.Postgres) *Repo {
	return &Repo{pg}
}

var _ repo.ChannelRepo = (*Repo)(nil)

func scanChannel(row pgx.Row) (entity.Channel, error) {
	var (
		c      entity.Channel
		status string
	)

	err := row.Scan(&c.ID, &c.OwnerID, &c.Handle, &c.DisplayName, &c.Bio, &c.AvatarURL, &status,
		&c.IsLive, &c.CurrentStreamID, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Channel{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("scan channel: %w", err)
	}

	c.Status = entity.Status(status)

	return c, nil
}

// inEventTx runs fn once per event id: a redelivered event finds its processed_events row and is skipped.
func (r *Repo) inEventTx(ctx context.Context, op, eventID string, fn func(tx pgx.Tx) error) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s - begin: %w", op, err)
	}

	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	if eventID != "" {
		tag, err := tx.Exec(ctx,
			`INSERT INTO processed_events (event_id, consumer) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			eventID, _consumer)
		if err != nil {
			return fmt.Errorf("%s - processed_events: %w", op, err)
		}

		if tag.RowsAffected() == 0 {
			return nil // duplicate delivery
		}
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s - commit: %w", op, err)
	}

	return nil
}

func addOutbox(ctx context.Context, tx pgx.Tx, channelID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox marshal: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES ('channel', $1, $2, $3)`,
		channelID, eventType, b)
	if err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}

	return nil
}

// ByHandle -.
func (r *Repo) ByHandle(ctx context.Context, handle string) (entity.Channel, error) {
	c, err := scanChannel(r.Pool.QueryRow(ctx, `SELECT `+_channelCols+` FROM channels WHERE handle = $1`, handle))
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return entity.Channel{}, fmt.Errorf("Repo - ByHandle: %w", err)
	}

	return c, err
}

// CreateForOwner -.
func (r *Repo) CreateForOwner(ctx context.Context, eventID string, in repo.NewChannel) (entity.Channel, bool, error) {
	var (
		ch      entity.Channel
		created bool
	)

	err := r.inEventTx(ctx, "Repo - CreateForOwner", eventID, func(tx pgx.Tx) error {
		var err error

		ch, created, err = createChannel(ctx, tx, in)
		if err != nil || !created {
			return err
		}

		return addOutbox(ctx, tx, ch.ID, events.TypeChannelCreated, events.ChannelCreated{
			ChannelID: ch.ID, OwnerID: ch.OwnerID, Handle: ch.Handle, DisplayName: ch.DisplayName,
		})
	})
	if err != nil {
		return entity.Channel{}, false, err
	}

	return ch, created, nil
}

// createChannel tries handle candidates in order. ON CONFLICT DO NOTHING covers both unique keys, so
// an empty insert is either the owner's existing channel (concurrent or earlier approval) or a
// taken handle; the owner lookup tells them apart without aborting the transaction.
func createChannel(ctx context.Context, tx pgx.Tx, in repo.NewChannel) (entity.Channel, bool, error) {
	for n := 1; n <= _maxHandleCandidates; n++ {
		existing, err := scanChannel(tx.QueryRow(ctx, `SELECT `+_channelCols+` FROM channels WHERE owner_id = $1`, in.OwnerID))
		if err == nil {
			return existing, false, nil
		}

		if !errors.Is(err, entity.ErrNotFound) {
			return entity.Channel{}, false, fmt.Errorf("Repo - CreateForOwner - owner: %w", err)
		}

		ch, err := scanChannel(tx.QueryRow(ctx,
			`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, $3)
			 ON CONFLICT DO NOTHING
			 RETURNING `+_channelCols,
			in.OwnerID, entity.HandleCandidate(in.BaseHandle, n), in.DisplayName))
		if err == nil {
			return ch, true, nil
		}

		if !errors.Is(err, entity.ErrNotFound) {
			return entity.Channel{}, false, fmt.Errorf("Repo - CreateForOwner - insert: %w", err)
		}
	}

	return entity.Channel{}, false, entity.ErrNoFreeHandle
}

// SetLive -.
func (r *Repo) SetLive(ctx context.Context, eventID, channelID, streamID string) error {
	err := r.inEventTx(ctx, "Repo - SetLive", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE channels SET is_live = true, current_stream_id = $2, updated_at = now() WHERE id = $1`,
			channelID, streamID); err != nil {
			return fmt.Errorf("Repo - SetLive: %w", err)
		}

		return nil
	})

	return err
}

// SetOffline -.
func (r *Repo) SetOffline(ctx context.Context, eventID, channelID, streamID string) error {
	err := r.inEventTx(ctx, "Repo - SetOffline", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE channels SET is_live = false, current_stream_id = NULL, updated_at = now()
			 WHERE id = $1 AND (current_stream_id IS NULL OR current_stream_id = $2)`,
			channelID, streamID); err != nil {
			return fmt.Errorf("Repo - SetOffline: %w", err)
		}

		return nil
	})

	return err
}
