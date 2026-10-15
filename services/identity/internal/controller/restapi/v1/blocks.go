package v1

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/response"
)

// listBlocks godoc
//
//	@Summary     Accounts the caller blocked
//	@Description Newest first. fields=ids returns only {userId} for every blocked account (at most 1000, one page): the web filters chat with it.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Param       fields query    string false "ids"
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "1-50" default(20)
//	@Success     200    {object} response.Data[response.BlockPage] "fields=ids: response.BlockedIDs"
//	@Failure     400    {object} response.Error "VALIDATION_FAILED (fields = ENUM, limit = MAX, cursor = INVALID)"
//	@Failure     401    {object} response.Error "UNAUTHORIZED"
//	@Router      /me/blocks [get]
func (r *V1) listBlocks(ctx *fiber.Ctx) error {
	userID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	var q request.ListBlocks
	if err := ctx.QueryParser(&q); err != nil {
		return validationFailed(ctx, map[string]string{"limit": "FORMAT"})
	}

	if errs := q.Validate(); len(errs) > 0 {
		return validationFailed(ctx, errs)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	if q.Fields == "ids" {
		ids, err := r.blocks.IDs(ctx.UserContext(), userID)
		if err != nil {
			return r.mapError(ctx, "listBlocks", err)
		}

		return ctx.JSON(response.Data[response.BlockedIDs]{Data: response.NewBlockedIDs(ids)})
	}

	page, err := r.blocks.List(ctx.UserContext(), userID, q.Cursor, q.Limit)
	if err != nil {
		return r.mapError(ctx, "listBlocks", err)
	}

	return ctx.JSON(response.Data[response.BlockPage]{Data: response.NewBlockPage(page)})
}

// block godoc
//
//	@Summary     Block an account
//	@Description Idempotent. The blocked user is not told; the web hides their chat messages and they cannot @mention-notify the caller.
//	@Tags        me
//	@Produce     json
//	@Security    BearerAuth
//	@Param       userId path string true "user id"
//	@Success     204
//	@Failure     400 {object} response.Error "VALIDATION_FAILED (userId = INVALID: yourself)"
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Failure     404 {object} response.Error "NOT_FOUND"
//	@Failure     409 {object} response.Error "CONFLICT (details.limit = 1000)"
//	@Router      /me/blocks/{userId} [put]
func (r *V1) block(ctx *fiber.Ctx) error {
	userID, target, ok := r.blockTarget(ctx)
	if !ok {
		return nil
	}

	if err := r.blocks.Block(ctx.UserContext(), userID, target); err != nil {
		return r.mapError(ctx, "block", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// unblock godoc
//
//	@Summary  Unblock an account
//	@Tags     me
//	@Security BearerAuth
//	@Param    userId path string true "user id"
//	@Success  204
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Failure  404 {object} response.Error "NOT_FOUND (malformed id)"
//	@Router   /me/blocks/{userId} [delete]
func (r *V1) unblock(ctx *fiber.Ctx) error {
	userID, target, ok := r.blockTarget(ctx)
	if !ok {
		return nil
	}

	if err := r.blocks.Unblock(ctx.UserContext(), userID, target); err != nil {
		return r.mapError(ctx, "unblock", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// blockTarget reads the caller and :userId; when ok is false the error response is already written.
func (r *V1) blockTarget(ctx *fiber.Ctx) (userID, target string, ok bool) {
	userID, ok = r.caller(ctx)
	if !ok {
		_ = errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil) //nolint:errcheck // written to ctx

		return "", "", false
	}

	id, err := uuid.Parse(ctx.Params("userId"))
	if err != nil {
		_ = errorJSON(ctx, http.StatusNotFound, CodeNotFound, "user not found", nil) //nolint:errcheck // written to ctx

		return "", "", false
	}

	return userID, id.String(), true
}

// adminUser godoc
//
//	@Summary     Admin: one account
//	@Description Status, lock (lockKind FAILED_LOGIN | ADMIN, lockedUntil null = permanent) and channel. An expired lock reads as ACTIVE.
//	@Tags        admin
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "user id"
//	@Success     200 {object} response.Data[response.AdminUser]
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Failure     403 {object} response.Error "FORBIDDEN"
//	@Failure     404 {object} response.Error "NOT_FOUND"
//	@Router      /admin/users/{id} [get]
func (r *V1) adminUser(ctx *fiber.Ctx) error {
	id, err := uuid.Parse(ctx.Params("id"))
	if err != nil {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "user not found", nil)
	}

	u, err := r.adminUsers.Get(ctx.UserContext(), id.String())
	if err != nil {
		return r.mapError(ctx, "adminUser", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.AdminUser]{Data: response.NewAdminUser(u)})
}
