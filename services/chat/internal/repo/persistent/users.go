package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// Users -.
func (r *Repo) Users(ctx context.Context, ids []string) (map[string]entity.User, error) {
	out := map[string]entity.User{}
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx,
		`SELECT id::text, username, display_name, email_verified FROM users WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("Repo - Users: %w", err)
	}

	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.User, error) {
		var u entity.User

		return u, row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.EmailVerified)
	})
	if err != nil {
		return nil, fmt.Errorf("Repo - Users: %w", err)
	}

	for _, u := range list {
		out[u.ID] = u
	}

	return out, nil
}

// UserIDsByUsername -.
func (r *Repo) UserIDsByUsername(ctx context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}
	if len(names) == 0 {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx, `SELECT lower(username), id::text FROM users WHERE lower(username) = ANY($1)`, names)
	if err != nil {
		return nil, fmt.Errorf("Repo - UserIDsByUsername: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, id string
		if err = rows.Scan(&name, &id); err != nil {
			return nil, fmt.Errorf("Repo - UserIDsByUsername - scan: %w", err)
		}

		out[name] = id
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - UserIDsByUsername: %w", err)
	}

	return out, nil
}

// UpsertUser -.
func (r *Repo) UpsertUser(ctx context.Context, eventID string, u entity.User) error {
	return r.inEventTx(ctx, "Repo - UpsertUser", eventID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO users (id, username, display_name, email_verified) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (id) DO UPDATE SET username = EXCLUDED.username, display_name = EXCLUDED.display_name,
			     email_verified = users.email_verified OR EXCLUDED.email_verified, updated_at = now()`,
			u.ID, u.Username, u.DisplayName, u.EmailVerified)
		if err != nil {
			return fmt.Errorf("Repo - UpsertUser: %w", err)
		}

		return nil
	})
}

// UpdateDisplayName does nothing for a user the copy does not have yet.
func (r *Repo) UpdateDisplayName(ctx context.Context, eventID, userID, displayName string) error {
	return r.inEventTx(ctx, "Repo - UpdateDisplayName", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1`, userID, displayName); err != nil {
			return fmt.Errorf("Repo - UpdateDisplayName: %w", err)
		}

		return nil
	})
}

// MarkEmailVerified -.
func (r *Repo) MarkEmailVerified(ctx context.Context, eventID, userID string) error {
	return r.inEventTx(ctx, "Repo - MarkEmailVerified", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET email_verified = true, updated_at = now() WHERE id = $1`, userID); err != nil {
			return fmt.Errorf("Repo - MarkEmailVerified: %w", err)
		}

		return nil
	})
}

// Channel -.
func (r *Repo) Channel(ctx context.Context, channelID string) (entity.Channel, error) {
	c := entity.Channel{ID: channelID}

	err := r.Pool.QueryRow(ctx, `SELECT owner_id::text FROM channels WHERE channel_id = $1`, channelID).Scan(&c.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Channel{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("Repo - Channel: %w", err)
	}

	return c, nil
}

// UpsertChannel -.
func (r *Repo) UpsertChannel(ctx context.Context, eventID string, c entity.Channel) error {
	return r.inEventTx(ctx, "Repo - UpsertChannel", eventID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO channels (channel_id, owner_id) VALUES ($1, $2)
			 ON CONFLICT (channel_id) DO UPDATE SET owner_id = EXCLUDED.owner_id, updated_at = now()`, c.ID, c.OwnerID)
		if err != nil {
			return fmt.Errorf("Repo - UpsertChannel: %w", err)
		}

		return nil
	})
}
