package persistent

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
)

// Overrides returns the stored rows (those that differ from the defaults) per user and type.
func (r *Repo) Overrides(ctx context.Context, userIDs []string) (map[string]map[entity.Type]entity.Setting, error) {
	out := make(map[string]map[entity.Type]entity.Setting, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx, `SELECT user_id::text, type, in_app, email FROM notification_settings
		WHERE user_id = ANY($1::uuid[])`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("SettingRepo - Overrides: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			userID, typ string
			s           entity.Setting
		)

		if err := rows.Scan(&userID, &typ, &s.InApp, &s.Email); err != nil {
			return nil, fmt.Errorf("SettingRepo - Overrides - scan: %w", err)
		}

		s.Type = entity.Type(typ)

		if out[userID] == nil {
			out[userID] = map[entity.Type]entity.Setting{}
		}

		out[userID][s.Type] = s
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("SettingRepo - Overrides - rows: %w", err)
	}

	return out, nil
}

// Save -.
func (r *Repo) Save(ctx context.Context, userID string, settings []entity.Setting) error {
	err := r.inTx(ctx, "SettingRepo - Save", func(tx pgx.Tx) error {
		for _, s := range settings {
			sql := `INSERT INTO notification_settings (user_id, type, in_app, email) VALUES ($1, $2, $3, $4)
				ON CONFLICT (user_id, type) DO UPDATE SET in_app = EXCLUDED.in_app, email = EXCLUDED.email`
			args := []any{userID, string(s.Type), s.InApp, s.Email}

			if s.IsDefault() {
				sql, args = `DELETE FROM notification_settings WHERE user_id = $1 AND type = $2`, args[:2]
			}

			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				return fmt.Errorf("save %s: %w", s.Type, err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("SettingRepo - Save: %w", err)
	}

	return nil
}
