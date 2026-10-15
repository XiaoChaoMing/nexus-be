package moderation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// List -.
func (uc *UseCase) List(ctx context.Context, p entity.Principal, channelID string, role *entity.ChannelRole, cursor string, limit int,
) (usecase.RolePage, error) {
	after, err := decodeCursor(cursor)
	if err != nil {
		return usecase.RolePage{}, err
	}

	if _, _, err = uc.manager(ctx, p, channelID); err != nil {
		return usecase.RolePage{}, err
	}

	limit = pageLimit(limit)

	items, err := uc.r.Roles.ListRoles(ctx, channelID, role, after, limit+1)
	if err != nil {
		return usecase.RolePage{}, fmt.Errorf("ModerationUseCase - List roles: %w", err)
	}

	page := usecase.RolePage{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.User.ID)
	}

	return page, nil
}

// Assign grants the role (at most entity.ChannelRoleMax users per role); the channel owner cannot get one.
func (uc *UseCase) Assign(ctx context.Context, p entity.Principal, channelID string, who usecase.RoleTarget, role entity.ChannelRole,
) (entity.RoleGrant, bool, error) {
	actor, ch, err := uc.manager(ctx, p, channelID)
	if err != nil {
		return entity.RoleGrant{}, false, err
	}

	userID, err := uc.resolve(ctx, who)
	if err != nil {
		return entity.RoleGrant{}, false, err
	}

	if userID == ch.OwnerID {
		field := "userId"
		if who.Username != "" {
			field = "username"
		}

		return entity.RoleGrant{}, false, entity.Reject(entity.ErrValidation, field, entity.DetailInvalid)
	}

	g, created, err := uc.r.Roles.Assign(ctx, entity.RoleGrant{
		ChannelID: channelID, User: entity.UserRef{ID: userID}, Role: role, GrantedBy: entity.UserRef{ID: p.UserID},
	}, entity.ChannelRoleMax, actor)
	if err != nil {
		switch {
		case errors.Is(err, entity.ErrLimitReached):
			return entity.RoleGrant{}, false, entity.Reject(entity.ErrLimitReached, "limit", fmt.Sprint(entity.ChannelRoleMax))
		case errors.Is(err, entity.ErrNotFound):
			return entity.RoleGrant{}, false, err
		}

		return entity.RoleGrant{}, false, fmt.Errorf("ModerationUseCase - Assign: %w", err)
	}

	uc.roles.drop(channelID + "/" + userID)

	return g, created, nil
}

// resolve returns the user id of the target; entity.ErrNotFound for a username the identity copy lacks.
func (uc *UseCase) resolve(ctx context.Context, who usecase.RoleTarget) (string, error) {
	if who.UserID != "" {
		if _, err := uc.knownUser(ctx, who.UserID); err != nil {
			return "", err
		}

		return who.UserID, nil
	}

	name := strings.ToLower(who.Username)

	ids, err := uc.r.Users.UserIDsByUsername(ctx, []string{name})
	if err != nil {
		return "", fmt.Errorf("ModerationUseCase - resolve: %w", err)
	}

	id, ok := ids[name]
	if !ok {
		return "", entity.ErrNotFound
	}

	return id, nil
}

// Revoke is idempotent.
func (uc *UseCase) Revoke(ctx context.Context, p entity.Principal, channelID, userID string) error {
	actor, _, err := uc.manager(ctx, p, channelID)
	if err != nil {
		return err
	}

	if _, err = uc.r.Roles.Revoke(ctx, channelID, userID, p.UserID, actor); err != nil {
		return fmt.Errorf("ModerationUseCase - Revoke: %w", err)
	}

	uc.roles.drop(channelID + "/" + userID)

	return nil
}

// Mine lists the channels where the user is moderator or VIP: live ones first, then by name.
func (uc *UseCase) Mine(ctx context.Context, userID string) ([]entity.ModChannel, error) {
	grants, err := uc.r.Roles.RolesOfUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ModerationUseCase - Mine: %w", err)
	}

	ids := make([]string, 0, len(grants))
	for _, g := range grants {
		ids = append(ids, g.ChannelID)
	}

	info, err := uc.r.Users.ChannelInfo(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("ModerationUseCase - Mine - channels: %w", err)
	}

	live, err := uc.r.Live.Live(ctx, ids)
	if err != nil {
		uc.l.Warn("moderation - live channels: %v", err)
	}

	out := make([]entity.ModChannel, 0, len(grants))

	for _, g := range grants {
		ch, ok := info[g.ChannelID]
		if !ok {
			ch = entity.ChannelInfo{ID: g.ChannelID, Status: entity.ChannelActive}
		}

		_, isLive := live[g.ChannelID]
		out = append(out, entity.ModChannel{Channel: ch, IsLive: isLive, Role: g.Role})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsLive != out[j].IsLive {
			return out[i].IsLive
		}

		return strings.ToLower(out[i].Channel.DisplayName) < strings.ToLower(out[j].Channel.DisplayName)
	})

	return out, nil
}
