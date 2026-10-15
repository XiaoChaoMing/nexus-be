package v1

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// listRoles godoc
//
//	@Summary  Moderators and VIPs of the channel (owner or admin)
//	@Tags     roles
//	@Produce  json
//	@Security BearerAuth
//	@Param    channelId path     string true  "channel id"
//	@Param    role      query    string false "MODERATOR | VIP"
//	@Param    cursor    query    string false "nextCursor of the previous page"
//	@Param    limit     query    int    false "1–50" default(24)
//	@Success  200       {object} response.Data[response.RolePage]
//	@Failure  400       {object} response.Error "VALIDATION_FAILED (role = ENUM)"
//	@Failure  403       {object} response.Error "FORBIDDEN"
//	@Failure  404       {object} response.Error "NOT_FOUND"
//	@Router   /channels/{channelId}/roles [get]
func (r *routes) listRoles(ctx *fiber.Ctx) error {
	channelID, q, ok := pageQuery(ctx)
	if !ok {
		return nil
	}

	var role *entity.ChannelRole

	if q.Role != "" {
		cr := entity.ChannelRole(q.Role)
		role = &cr
	}

	page, err := r.roles.List(ctx.UserContext(), principal(ctx), channelID, role, q.Cursor, q.Limit)
	if err != nil {
		return r.fail(ctx, "listRoles", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.RolePage]{Data: response.RolePage{Items: mapItems(page.Items, roleOf), NextCursor: page.NextCursor}})
}

// assignRole godoc
//
//	@Summary     Make a user moderator or VIP (owner or admin)
//	@Description By username. 201 for a new role, 200 when it changed or was already given. At most 50 users per role.
//	@Tags        roles
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string             true "channel id"
//	@Param       body      body     request.AssignRole true "user and role"
//	@Success     201       {object} response.Data[response.ChannelRole]
//	@Success     200       {object} response.Data[response.ChannelRole]
//	@Failure     400       {object} response.Error "VALIDATION_FAILED (username = REQUIRED | INVALID, role = ENUM)"
//	@Failure     403       {object} response.Error "FORBIDDEN"
//	@Failure     404       {object} response.Error "NOT_FOUND"
//	@Failure     409       {object} response.Error "CONFLICT (details.limit = 50)"
//	@Router      /channels/{channelId}/roles [post]
func (r *routes) assignRole(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId")
	if !ok {
		return nil
	}

	var body request.AssignRole
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if errs := body.Validate(); len(errs) > 0 {
		return validationFailed(ctx, errs)
	}

	return r.assign(ctx, p[0], usecase.RoleTarget{Username: body.Username}, entity.ChannelRole(body.Role))
}

// setRole godoc
//
//	@Summary  Set a user's role by id (owner or admin)
//	@Tags     roles
//	@Accept   json
//	@Produce  json
//	@Security BearerAuth
//	@Param    channelId path     string          true "channel id"
//	@Param    userId    path     string          true "user id"
//	@Param    body      body     request.SetRole true "role"
//	@Success  201       {object} response.Data[response.ChannelRole]
//	@Success  200       {object} response.Data[response.ChannelRole]
//	@Failure  400       {object} response.Error "VALIDATION_FAILED (role = ENUM, userId = INVALID)"
//	@Failure  403       {object} response.Error "FORBIDDEN"
//	@Failure  404       {object} response.Error "NOT_FOUND"
//	@Failure  409       {object} response.Error "CONFLICT (details.limit = 50)"
//	@Router   /channels/{channelId}/roles/{userId} [put]
func (r *routes) setRole(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId", "userId")
	if !ok {
		return nil
	}

	var body request.SetRole
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if errs := body.Validate(); len(errs) > 0 {
		return validationFailed(ctx, errs)
	}

	return r.assign(ctx, p[0], usecase.RoleTarget{UserID: p[1]}, entity.ChannelRole(body.Role))
}

func (r *routes) assign(ctx *fiber.Ctx, channelID string, who usecase.RoleTarget, role entity.ChannelRole) error {
	g, created, err := r.roles.Assign(ctx.UserContext(), principal(ctx), channelID, who, role)
	if err != nil {
		return r.fail(ctx, "assignRole", err)
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	return ctx.Status(status).JSON(response.Data[response.ChannelRole]{Data: roleOf(g)})
}

// revokeRole godoc
//
//	@Summary  Remove a user's role (owner or admin)
//	@Tags     roles
//	@Security BearerAuth
//	@Param    channelId path string true "channel id"
//	@Param    userId    path string true "user id"
//	@Success  204
//	@Failure  403 {object} response.Error "FORBIDDEN"
//	@Router   /channels/{channelId}/roles/{userId} [delete]
func (r *routes) revokeRole(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId", "userId")
	if !ok {
		return nil
	}

	if err := r.roles.Revoke(ctx.UserContext(), principal(ctx), p[0], p[1]); err != nil {
		return r.fail(ctx, "revokeRole", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// myChannelRoles godoc
//
//	@Summary  Channels the caller moderates (or is VIP of)
//	@Tags     roles
//	@Produce  json
//	@Security BearerAuth
//	@Success  200 {object} response.Data[response.ModChannelList]
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Router   /me/channel-roles [get]
func (r *routes) myChannelRoles(ctx *fiber.Ctx) error {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, entity.CodeUnauthorized, "unauthorized", nil)
	}

	list, err := r.roles.Mine(ctx.UserContext(), userID)
	if err != nil {
		return r.fail(ctx, "myChannelRoles", err)
	}

	out := response.ModChannelList{Items: make([]response.ModChannel, 0, len(list))}
	for _, m := range list {
		out.Items = append(out.Items, response.ModChannel{
			Channel: response.ModChannelRef{
				ID: m.Channel.ID, Handle: m.Channel.Handle, DisplayName: m.Channel.DisplayName, AvatarURL: m.Channel.AvatarURL, IsLive: m.IsLive,
			},
			Role: string(m.Role),
		})
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.ModChannelList]{Data: out})
}

func roleOf(g entity.RoleGrant) response.ChannelRole {
	return response.ChannelRole{
		User: userRefOf(g.User), Role: string(g.Role), GrantedBy: response.ActorRef{ID: g.GrantedBy.ID, Username: g.GrantedBy.Username},
		CreatedAt: g.CreatedAt.UTC(),
	}
}
