package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
)

const _grantCols = `g.channel_id::text, g.user_id::text, COALESCE(u.username, ''), COALESCE(u.display_name, ''), g.role,
	g.granted_by::text, COALESCE(b.username, ''), COALESCE(b.display_name, ''), g.created_at`

const _grantFrom = ` FROM channel_roles g LEFT JOIN users u ON u.id = g.user_id LEFT JOIN users b ON b.id = g.granted_by`

func scanGrant(row pgx.CollectableRow) (entity.RoleGrant, error) {
	var (
		g    entity.RoleGrant
		role string
	)

	err := row.Scan(&g.ChannelID, &g.User.ID, &g.User.Username, &g.User.DisplayName, &role,
		&g.GrantedBy.ID, &g.GrantedBy.Username, &g.GrantedBy.DisplayName, &g.CreatedAt)
	g.Role = entity.ChannelRole(role)

	return g, err
}

// ListRoles -.
func (r *Repo) ListRoles(ctx context.Context, channelID string, role *entity.ChannelRole, after *repo.Cursor, limit int,
) ([]entity.RoleGrant, error) {
	q := `SELECT ` + _grantCols + _grantFrom + ` WHERE g.channel_id = $1`
	args := []any{channelID, limit}

	if role != nil {
		args = append(args, string(*role))
		q += fmt.Sprintf(` AND g.role = $%d`, len(args))
	}

	if after != nil {
		args = append(args, after.CreatedAt, after.ID)
		q += fmt.Sprintf(` AND (g.created_at, g.user_id) < ($%d, $%d::uuid)`, len(args)-1, len(args))
	}

	rows, err := r.Pool.Query(ctx, q+` ORDER BY g.created_at DESC, g.user_id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - ListRoles: %w", err)
	}

	out, err := pgx.CollectRows(rows, scanGrant)
	if err != nil {
		return nil, fmt.Errorf("Repo - ListRoles: %w", err)
	}

	return out, nil
}

// Assign -.
func (r *Repo) Assign(ctx context.Context, g entity.RoleGrant, maxPerRole int, actorRole entity.ActorRole) (entity.RoleGrant, bool, error) {
	var (
		out     entity.RoleGrant
		created bool
	)

	err := r.inTx(ctx, "Repo - Assign", func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `SELECT 1 FROM channels WHERE channel_id = $1 FOR UPDATE`, g.ChannelID)
		if err != nil {
			return fmt.Errorf("Repo - Assign - lock: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return entity.ErrNotFound
		}

		prev, err := currentRole(ctx, tx, g.ChannelID, g.User.ID)
		if err != nil {
			return err
		}

		if prev == nil || *prev != g.Role {
			if gerr := grantTx(ctx, tx, g, prev, maxPerRole, actorRole); gerr != nil {
				return gerr
			}
		}

		created = prev == nil
		out, err = readGrant(ctx, tx, g.ChannelID, g.User.ID)
		out.Previous = prev

		return err
	})

	return out, created, err
}

func readGrant(ctx context.Context, tx pgx.Tx, channelID, userID string) (entity.RoleGrant, error) {
	rows, err := tx.Query(ctx, `SELECT `+_grantCols+_grantFrom+` WHERE g.channel_id = $1 AND g.user_id = $2`, channelID, userID)
	if err != nil {
		return entity.RoleGrant{}, fmt.Errorf("Repo - Assign - read: %w", err)
	}

	g, err := pgx.CollectExactlyOneRow(rows, scanGrant)
	if err != nil {
		return entity.RoleGrant{}, fmt.Errorf("Repo - Assign - read: %w", err)
	}

	return g, nil
}

func currentRole(ctx context.Context, tx pgx.Tx, channelID, userID string) (*entity.ChannelRole, error) {
	var role string

	err := tx.QueryRow(ctx, `SELECT role FROM channel_roles WHERE channel_id = $1 AND user_id = $2`, channelID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // no role
	}

	if err != nil {
		return nil, fmt.Errorf("current role: %w", err)
	}

	cr := entity.ChannelRole(role)

	return &cr, nil
}

// grantTx stores the new role after checking the role's limit and queues channel.role_changed.v1.
func grantTx(ctx context.Context, tx pgx.Tx, g entity.RoleGrant, prev *entity.ChannelRole, maxPerRole int, actorRole entity.ActorRole) error {
	var n int

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_roles WHERE channel_id = $1 AND role = $2 AND user_id <> $3`,
		g.ChannelID, string(g.Role), g.User.ID).Scan(&n); err != nil {
		return fmt.Errorf("Repo - Assign - count: %w", err)
	}

	if n >= maxPerRole {
		return entity.ErrLimitReached
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO channel_roles (channel_id, user_id, role, granted_by) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (channel_id, user_id) DO UPDATE SET role = EXCLUDED.role, granted_by = EXCLUDED.granted_by, created_at = now()`,
		g.ChannelID, g.User.ID, string(g.Role), g.GrantedBy.ID); err != nil {
		return fmt.Errorf("Repo - Assign - upsert: %w", err)
	}

	role := string(g.Role)

	return addOutboxAs(ctx, tx, _aggregateChannel, g.ChannelID, events.TypeChannelRoleChanged, events.ChannelRoleChanged{
		ChannelID: g.ChannelID, UserID: g.User.ID, Role: &role, PreviousRole: (*string)(prev),
		ActorID: g.GrantedBy.ID, ActorRole: string(actorRole),
	})
}

// Revoke -.
func (r *Repo) Revoke(ctx context.Context, channelID, userID, actorID string, actorRole entity.ActorRole) (bool, error) {
	removed := false

	err := r.inTx(ctx, "Repo - Revoke", func(tx pgx.Tx) error {
		var role string

		err := tx.QueryRow(ctx, `DELETE FROM channel_roles WHERE channel_id = $1 AND user_id = $2 RETURNING role`,
			channelID, userID).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("Repo - Revoke: %w", err)
		}

		removed = true

		return addOutboxAs(ctx, tx, _aggregateChannel, channelID, events.TypeChannelRoleChanged, events.ChannelRoleChanged{
			ChannelID: channelID, UserID: userID, PreviousRole: &role, ActorID: actorID, ActorRole: string(actorRole),
		})
	})

	return removed, err
}

// RolesOfUser -.
func (r *Repo) RolesOfUser(ctx context.Context, userID string) ([]entity.RoleGrant, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_grantCols+_grantFrom+` WHERE g.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("Repo - RolesOfUser: %w", err)
	}

	out, err := pgx.CollectRows(rows, scanGrant)
	if err != nil {
		return nil, fmt.Errorf("Repo - RolesOfUser: %w", err)
	}

	return out, nil
}
