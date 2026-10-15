package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
)

const _maxLimit = 50

// list -.
//
//	@Summary     List my notifications
//	@Description Most recently updated first (a grouped notification moves up when it grows). unread=true keeps the unread ones. Keyset paging with nextCursor.
//	@Tags        notifications
//	@Produce     json
//	@Security    BearerAuth
//	@Param       unread query    bool   false "only unread"
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "1–50, default 20"
//	@Success     200    {object} Data[NotificationList]
//	@Failure     400    {object} ErrorResponse "VALIDATION_FAILED (unread=FORMAT, limit=FORMAT, cursor=INVALID)"
//	@Failure     401    {object} ErrorResponse "UNAUTHORIZED"
//	@Router      /notifications [get]
func (r *routes) list(ctx *fiber.Ctx) error {
	userID, err := caller(ctx)
	if err != nil {
		return err
	}

	unread := false
	if s := ctx.Query("unread"); s != "" {
		if unread, err = strconv.ParseBool(s); err != nil {
			return validationFailed(ctx, map[string]string{"unread": entity.DetailFormat})
		}
	}

	limit := 0
	if s := ctx.Query("limit"); s != "" {
		if limit, err = strconv.Atoi(s); err != nil || limit < 1 || limit > _maxLimit {
			return validationFailed(ctx, map[string]string{"limit": entity.DetailFormat})
		}
	}

	p, err := r.uc.Center.List(ctx.UserContext(), userID, unread, ctx.Query("cursor"), limit)
	if err != nil {
		return r.fail(ctx, "list", err)
	}

	out := NotificationList{Items: make([]Notification, 0, len(p.Items)), NextCursor: p.NextCursor}
	for _, n := range p.Items {
		out.Items = append(out.Items, notificationOf(n))
	}

	return ctx.JSON(Data[NotificationList]{Data: out})
}

// unreadCount -.
//
//	@Summary     My unread count (bell)
//	@Tags        notifications
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} Data[UnreadCount]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Router      /notifications/unread-count [get]
func (r *routes) unreadCount(ctx *fiber.Ctx) error {
	userID, err := caller(ctx)
	if err != nil {
		return err
	}

	n, err := r.uc.Center.UnreadCount(ctx.UserContext(), userID)
	if err != nil {
		return r.fail(ctx, "unreadCount", err)
	}

	return ctx.JSON(Data[UnreadCount]{Data: UnreadCount{Count: n}})
}

// read -.
//
//	@Summary     Mark one notification read
//	@Description Already read → 204. Another user's, deleted or unknown → 404. Other tabs get unread_count on user:{userId}.
//	@Tags        notifications
//	@Security    BearerAuth
//	@Param       id  path string true "notification id"
//	@Success     204
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Router      /notifications/{id}/read [post]
func (r *routes) read(ctx *fiber.Ctx) error {
	userID, err := caller(ctx)
	if err != nil {
		return err
	}

	if err := r.uc.Center.Read(ctx.UserContext(), userID, ctx.Params("id")); err != nil {
		return r.fail(ctx, "read", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// readAll -.
//
//	@Summary     Mark all my notifications read
//	@Description Only those updated at or before `before` (default now), so a notification arriving meanwhile stays unread.
//	@Tags        notifications
//	@Accept      json
//	@Security    BearerAuth
//	@Param       body body ReadAllRequest false "optional before"
//	@Success     204
//	@Failure     400 {object} ErrorResponse "VALIDATION_FAILED (before=FORMAT)"
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Router      /notifications/read-all [post]
func (r *routes) readAll(ctx *fiber.Ctx) error {
	userID, err := caller(ctx)
	if err != nil {
		return err
	}

	var body ReadAllRequest
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(&body); err != nil {
			return validationFailed(ctx, map[string]string{"before": entity.DetailFormat})
		}
	}

	var before time.Time
	if body.Before != nil {
		if before, err = time.Parse(time.RFC3339, *body.Before); err != nil {
			return validationFailed(ctx, map[string]string{"before": entity.DetailFormat})
		}
	}

	if err := r.uc.Center.ReadAll(ctx.UserContext(), userID, before); err != nil {
		return r.fail(ctx, "readAll", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// remove -.
//
//	@Summary     Delete one notification
//	@Description Soft delete. Already deleted → 204; another user's or unknown → 404.
//	@Tags        notifications
//	@Security    BearerAuth
//	@Param       id  path string true "notification id"
//	@Success     204
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Router      /notifications/{id} [delete]
func (r *routes) remove(ctx *fiber.Ctx) error {
	userID, err := caller(ctx)
	if err != nil {
		return err
	}

	if err := r.uc.Center.Delete(ctx.UserContext(), userID, ctx.Params("id")); err != nil {
		return r.fail(ctx, "remove", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}
