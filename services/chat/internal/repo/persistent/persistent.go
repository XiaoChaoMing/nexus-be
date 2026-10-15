// Package persistent implements chat-svc storage on PostgreSQL (database nxl_chat).
package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
)

const (
	_consumer         = "chat-svc"
	_aggregateChannel = "channel"
)

// Repo implements the PostgreSQL contracts of repo.
type Repo struct {
	*postgres.Postgres
}

var (
	_ repo.RoomRepo     = (*Repo)(nil)
	_ repo.MessageRepo  = (*Repo)(nil)
	_ repo.SettingsRepo = (*Repo)(nil)
	_ repo.RoleRepo     = (*Repo)(nil)
	_ repo.EmoteRepo    = (*Repo)(nil)
	_ repo.UserRepo     = (*Repo)(nil)
)

// New -.
func New(pg *postgres.Postgres) *Repo {
	return &Repo{pg}
}

func (r *Repo) inTx(ctx context.Context, op string, fn func(tx pgx.Tx) error) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s - begin: %w", op, err)
	}

	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s - commit: %w", op, err)
	}

	return nil
}

// inEventTx runs fn once per event id: a redelivered event finds its processed_events row and is skipped.
func (r *Repo) inEventTx(ctx context.Context, op, eventID string, fn func(tx pgx.Tx) error) error {
	return r.inTx(ctx, op, func(tx pgx.Tx) error {
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

		return fn(tx)
	})
}

// ---- rooms ---------------------------------------------------------------------------------------

// _sendContextSQL reads the room with its channel's owner and settings and, for $2 (NULL for guests),
// the user's role, profile and restriction: the latest of BAN / UNBAN / TIMEOUT / UNTIMEOUT decides (a new
// timeout replaces a longer one, UNTIMEOUT lifts it, BAN outlasts timeouts until UNBAN).
const _sendContextSQL = `WITH me AS (SELECT NULLIF($2, '')::uuid AS id)
	SELECT r.channel_id::text, r.status, c.owner_id::text,
	       s.slow_sec, s.followers_min, s.subs_only, s.block_links, s.updated_at,
	       cr.role, u.username, u.display_name, u.email_verified,
	       COALESCE(last.type = 'BAN', false),
	       CASE WHEN last.type = 'TIMEOUT' AND last.expires_at > now() THEN last.expires_at END
	FROM chat_rooms r CROSS JOIN me
	LEFT JOIN channels c ON c.channel_id = r.channel_id
	LEFT JOIN chat_settings s ON s.channel_id = r.channel_id
	LEFT JOIN channel_roles cr ON cr.channel_id = r.channel_id AND cr.user_id = me.id
	LEFT JOIN users u ON u.id = me.id
	LEFT JOIN LATERAL (
		SELECT a.type, a.expires_at FROM mod_actions a
		WHERE a.channel_id = r.channel_id AND a.target_user_id = me.id
		  AND a.type IN ('BAN', 'UNBAN', 'TIMEOUT', 'UNTIMEOUT')
		ORDER BY a.created_at DESC, a.id DESC LIMIT 1) last ON true
	WHERE r.stream_id = $1`

// sendContextRow holds the nullable columns of _sendContextSQL.
type sendContextRow struct {
	status                string
	owner, role           *string
	slow                  *int
	subsOnly, blockLinks  *bool
	settingsAt            *time.Time
	username, displayName *string
	verified              *bool
}

// SendContext -.
func (r *Repo) SendContext(ctx context.Context, streamID, userID string) (entity.SendContext, error) {
	var (
		sc  entity.SendContext
		row sendContextRow
	)

	err := r.Pool.QueryRow(ctx, _sendContextSQL, streamID, userID).
		Scan(&sc.Room.ChannelID, &row.status, &row.owner, &row.slow, &sc.Settings.FollowersMin, &row.subsOnly, &row.blockLinks, &row.settingsAt,
			&row.role, &row.username, &row.displayName, &row.verified, &sc.Restriction.Banned, &sc.Restriction.TimedOutUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.SendContext{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.SendContext{}, fmt.Errorf("Repo - SendContext: %w", err)
	}

	sc.Room.StreamID, sc.Room.Status = streamID, entity.RoomStatus(row.status)
	sc.Settings.ChannelID, sc.Settings.UpdatedAt = sc.Room.ChannelID, row.settingsAt
	sc.Settings.SlowSec = derefOr(row.slow, 0)
	sc.Settings.SubsOnly = derefOr(row.subsOnly, false)
	sc.Settings.BlockLinks = derefOr(row.blockLinks, false)
	sc.OwnerID = derefOr(row.owner, "")

	if row.role != nil {
		cr := entity.ChannelRole(*row.role)
		sc.Role = &cr
	}

	if row.username != nil {
		sc.User = &entity.User{
			ID: userID, Username: *row.username, DisplayName: derefOr(row.displayName, ""), EmailVerified: derefOr(row.verified, false),
		}
	}

	return sc, nil
}

func derefOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}

	return *p
}

// OpenRoom -.
func (r *Repo) OpenRoom(ctx context.Context, room entity.Room) (bool, error) {
	tag, err := r.Pool.Exec(ctx,
		`INSERT INTO chat_rooms (stream_id, channel_id, status, opened_at) VALUES ($1, $2, 'OPEN', now())
		 ON CONFLICT (stream_id) DO NOTHING`, room.StreamID, room.ChannelID)
	if err != nil {
		return false, fmt.Errorf("Repo - OpenRoom: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

// CloseRoom -.
func (r *Repo) CloseRoom(ctx context.Context, streamID string, peakViewers int) (bool, error) {
	closed := false

	err := r.inTx(ctx, "Repo - CloseRoom", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE chat_rooms SET status = 'READ_ONLY', closed_at = now() WHERE stream_id = $1 AND status = 'OPEN'`, streamID)
		if err != nil {
			return fmt.Errorf("Repo - CloseRoom - update: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return nil
		}

		closed = true

		return addOutbox(ctx, tx, streamID, events.TypeStreamPeakViewers,
			events.StreamPeakViewers{StreamID: streamID, PeakViewers: peakViewers})
	})

	return closed, err
}

func addOutbox(ctx context.Context, tx pgx.Tx, aggregateID, eventType string, payload any) error {
	return addOutboxAs(ctx, tx, "chat_room", aggregateID, eventType, payload)
}

func addOutboxAs(ctx context.Context, tx pgx.Tx, aggregateType, aggregateID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox marshal: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES ($1, $2, $3, $4)`,
		aggregateType, aggregateID, eventType, b)
	if err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}

	return nil
}

// OpenRoomsOfChannel -.
func (r *Repo) OpenRoomsOfChannel(ctx context.Context, channelID string) ([]string, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT stream_id::text FROM chat_rooms WHERE channel_id = $1 AND status = 'OPEN'`, channelID)
	if err != nil {
		return nil, fmt.Errorf("Repo - OpenRoomsOfChannel: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("Repo - OpenRoomsOfChannel: %w", err)
	}

	return ids, nil
}

// ---- settings ------------------------------------------------------------------------------------

// Settings -.
func (r *Repo) Settings(ctx context.Context, channelID string) (entity.ChatSettings, error) {
	s := entity.ChatSettings{ChannelID: channelID}

	var at time.Time

	err := r.Pool.QueryRow(ctx,
		`SELECT slow_sec, followers_min, subs_only, block_links, updated_by::text, updated_at FROM chat_settings WHERE channel_id = $1`,
		channelID).Scan(&s.SlowSec, &s.FollowersMin, &s.SubsOnly, &s.BlockLinks, &s.UpdatedBy, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}

	if err != nil {
		return entity.ChatSettings{}, fmt.Errorf("Repo - Settings: %w", err)
	}

	s.UpdatedAt = &at

	return s, nil
}

// SaveSettings -.
func (r *Repo) SaveSettings(ctx context.Context, s entity.ChatSettings, change *events.ChatSettingsChanged) (entity.ChatSettings, error) {
	var at time.Time

	err := r.inTx(ctx, "Repo - SaveSettings", func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`INSERT INTO chat_settings (channel_id, slow_sec, followers_min, subs_only, block_links, updated_by, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, now())
			 ON CONFLICT (channel_id) DO UPDATE
			 SET slow_sec = EXCLUDED.slow_sec, followers_min = EXCLUDED.followers_min, subs_only = EXCLUDED.subs_only,
			     block_links = EXCLUDED.block_links, updated_by = EXCLUDED.updated_by, updated_at = now()
			 RETURNING updated_at`,
			s.ChannelID, s.SlowSec, s.FollowersMin, s.SubsOnly, s.BlockLinks, s.UpdatedBy).Scan(&at)
		if err != nil {
			return fmt.Errorf("Repo - SaveSettings: %w", err)
		}

		if change == nil {
			return nil
		}

		return addOutboxAs(ctx, tx, _aggregateChannel, s.ChannelID, events.TypeChatSettingsChanged, change)
	})
	if err != nil {
		return entity.ChatSettings{}, err
	}

	s.UpdatedAt = &at

	return s, nil
}

// ---- roles ---------------------------------------------------------------------------------------

// Roles -.
func (r *Repo) Roles(ctx context.Context, channelID string, userIDs []string) (map[string]entity.ChannelRole, error) {
	out := map[string]entity.ChannelRole{}
	if len(userIDs) == 0 {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx,
		`SELECT user_id::text, role FROM channel_roles WHERE channel_id = $1 AND user_id = ANY($2::uuid[])`, channelID, userIDs)
	if err != nil {
		return nil, fmt.Errorf("Repo - Roles: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, role string
		if err = rows.Scan(&id, &role); err != nil {
			return nil, fmt.Errorf("Repo - Roles - scan: %w", err)
		}

		out[id] = entity.ChannelRole(role)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - Roles: %w", err)
	}

	return out, nil
}

// GrantRole -.
func (r *Repo) GrantRole(ctx context.Context, channelID, userID string, role entity.ChannelRole, grantedBy string) error {
	_, err := r.Pool.Exec(ctx,
		`INSERT INTO channel_roles (channel_id, user_id, role, granted_by) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (channel_id, user_id) DO UPDATE SET role = EXCLUDED.role, granted_by = EXCLUDED.granted_by`,
		channelID, userID, string(role), grantedBy)
	if err != nil {
		return fmt.Errorf("Repo - GrantRole: %w", err)
	}

	return nil
}

// RevokeRole -.
func (r *Repo) RevokeRole(ctx context.Context, channelID, userID string) error {
	if _, err := r.Pool.Exec(ctx, `DELETE FROM channel_roles WHERE channel_id = $1 AND user_id = $2`, channelID, userID); err != nil {
		return fmt.Errorf("Repo - RevokeRole: %w", err)
	}

	return nil
}

// ---- emotes --------------------------------------------------------------------------------------

// GlobalEmotes -.
func (r *Repo) GlobalEmotes(ctx context.Context) ([]entity.Emote, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT id::text, code, image_url, animated, scope FROM emotes WHERE scope = 'GLOBAL' ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("Repo - GlobalEmotes: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.Emote, error) {
		var e entity.Emote

		return e, row.Scan(&e.ID, &e.Code, &e.ImageURL, &e.Animated, &e.Scope)
	})
	if err != nil {
		return nil, fmt.Errorf("Repo - GlobalEmotes: %w", err)
	}

	return out, nil
}
