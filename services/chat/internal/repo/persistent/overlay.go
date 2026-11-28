package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// Overlay -.
func (r *Repo) Overlay(ctx context.Context, channelID string) (entity.OverlaySettings, error) {
	var (
		chat, alert []byte
		at          time.Time
		by          *string
	)

	err := r.Pool.QueryRow(ctx,
		`SELECT chat, alert, updated_by::text, updated_at FROM overlay_settings WHERE channel_id = $1`,
		channelID).Scan(&chat, &alert, &by, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.DefaultOverlaySettings(channelID), nil
	}

	if err != nil {
		return entity.OverlaySettings{}, fmt.Errorf("Repo - Overlay: %w", err)
	}

	s := entity.OverlaySettings{ChannelID: channelID, UpdatedBy: by, UpdatedAt: &at}

	if err = json.Unmarshal(chat, &s.Chat); err != nil {
		return entity.OverlaySettings{}, fmt.Errorf("Repo - Overlay - chat: %w", err)
	}

	if err = json.Unmarshal(alert, &s.Alert); err != nil {
		return entity.OverlaySettings{}, fmt.Errorf("Repo - Overlay - alert: %w", err)
	}

	return s, nil
}

// SaveOverlay -.
func (r *Repo) SaveOverlay(ctx context.Context, s entity.OverlaySettings) (entity.OverlaySettings, error) {
	chat, err := json.Marshal(s.Chat)
	if err != nil {
		return entity.OverlaySettings{}, fmt.Errorf("Repo - SaveOverlay - chat: %w", err)
	}

	alert, err := json.Marshal(s.Alert)
	if err != nil {
		return entity.OverlaySettings{}, fmt.Errorf("Repo - SaveOverlay - alert: %w", err)
	}

	var at time.Time

	err = r.Pool.QueryRow(ctx,
		`INSERT INTO overlay_settings (channel_id, chat, alert, updated_by, updated_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (channel_id) DO UPDATE
		 SET chat = EXCLUDED.chat, alert = EXCLUDED.alert, updated_by = EXCLUDED.updated_by, updated_at = now()
		 RETURNING updated_at`,
		s.ChannelID, chat, alert, s.UpdatedBy).Scan(&at)
	if err != nil {
		return entity.OverlaySettings{}, fmt.Errorf("Repo - SaveOverlay: %w", err)
	}

	s.UpdatedAt = &at

	return s, nil
}
