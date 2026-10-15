package v1

import (
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

// principal is the authenticated caller (Required) or a guest (Optional without a token).
func principal(ctx *fiber.Ctx) entity.Principal {
	userID, _ := middleware.UserID(ctx)

	return entity.Principal{UserID: userID, GlobalRole: middleware.Role(ctx), IP: ctx.IP()}
}

// ids reads uuid path parameters; ok is false (and 404 written) when one is malformed.
func ids(ctx *fiber.Ctx, names ...string) ([]string, bool) {
	out := make([]string, 0, len(names))

	for _, n := range names {
		u, err := uuid.Parse(ctx.Params(n))
		if err != nil {
			_ = errorJSON(ctx, http.StatusNotFound, entity.CodeNotFound, "not found", nil) //nolint:errcheck // written to ctx

			return nil, false
		}

		out = append(out, u.String())
	}

	return out, true
}

// pageQuery reads {channelId} and the list query; when ok is false the error response is written.
func pageQuery(ctx *fiber.Ctx) (string, request.Page, bool) {
	var q request.Page

	p, ok := ids(ctx, "channelId")
	if !ok {
		return "", q, false
	}

	if err := ctx.QueryParser(&q); err != nil {
		_ = validationFailed(ctx, map[string]string{"limit": entity.DetailFormat}) //nolint:errcheck // written to ctx

		return "", q, false
	}

	if errs := q.Validate(); len(errs) > 0 {
		_ = validationFailed(ctx, errs) //nolint:errcheck // written to ctx

		return "", q, false
	}

	return p[0], q, true
}

// deleteMessage godoc
//
//	@Summary     Delete a chat message (moderation)
//	@Description Channel owner, moderators and admins. Every viewer gets `delete {messageId}` (and `unpin` when it was pinned). Deleting a deleted message is a no-op.
//	@Tags        moderation
//	@Security    BearerAuth
//	@Param       streamId  path string true "stream id"
//	@Param       messageId path string true "message id"
//	@Success     204
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Failure     403 {object} response.Error "FORBIDDEN (details.target = PROTECTED: the owner's or another moderator's message)"
//	@Failure     404 {object} response.Error "NOT_FOUND"
//	@Router      /chat/rooms/{streamId}/messages/{messageId} [delete]
func (r *routes) deleteMessage(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "streamId", "messageId")
	if !ok {
		return nil
	}

	if err := r.mod.DeleteMessage(ctx.UserContext(), principal(ctx), p[0], p[1]); err != nil {
		return r.fail(ctx, "deleteMessage", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// pin godoc
//
//	@Summary     Pin a message (one per room)
//	@Description Replaces the current pin; every viewer gets `pin {message}`.
//	@Tags        moderation
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       streamId path     string      true "stream id"
//	@Param       body     body     request.Pin true "message to pin"
//	@Success     200      {object} response.Data[response.Pinned]
//	@Failure     400      {object} response.Error "VALIDATION_FAILED (messageId = REQUIRED | INVALID)"
//	@Failure     403      {object} response.Error "FORBIDDEN"
//	@Failure     404      {object} response.Error "NOT_FOUND"
//	@Failure     409      {object} response.Error "INVALID_STATE (system or deleted message), CHAT_READ_ONLY"
//	@Router      /chat/rooms/{streamId}/pin [put]
func (r *routes) pin(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "streamId")
	if !ok {
		return nil
	}

	var body request.Pin
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if errs := body.Validate(); len(errs) > 0 {
		return validationFailed(ctx, errs)
	}

	m, err := r.mod.Pin(ctx.UserContext(), principal(ctx), p[0], body.MessageID)
	if err != nil {
		return r.fail(ctx, "pin", err)
	}

	return ctx.JSON(response.Data[response.Pinned]{Data: response.Pinned{Message: messageOf(m)}})
}

// unpin godoc
//
//	@Summary  Unpin the room's message
//	@Tags     moderation
//	@Security BearerAuth
//	@Param    streamId path string true "stream id"
//	@Success  204
//	@Failure  403 {object} response.Error "FORBIDDEN"
//	@Failure  404 {object} response.Error "NOT_FOUND"
//	@Router   /chat/rooms/{streamId}/pin [delete]
func (r *routes) unpin(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "streamId")
	if !ok {
		return nil
	}

	if err := r.mod.Unpin(ctx.UserContext(), principal(ctx), p[0]); err != nil {
		return r.fail(ctx, "unpin", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// timeout godoc
//
//	@Summary     Time a user out in the channel
//	@Description Hides the user's messages in the open rooms (`user_cleared`), and refuses their messages until `until` (CHAT_TIMED_OUT). A new timeout replaces the running one.
//	@Tags        moderation
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string          true "channel id"
//	@Param       userId    path     string          true "user id"
//	@Param       body      body     request.Timeout true "duration (1–1209600 s) and reason"
//	@Success     200       {object} response.Data[response.Restriction]
//	@Failure     400       {object} response.Error "VALIDATION_FAILED (durationSec = REQUIRED | MIN | MAX, reason = LENGTH, userId = INVALID)"
//	@Failure     403       {object} response.Error "FORBIDDEN (details.target = PROTECTED)"
//	@Failure     404       {object} response.Error "NOT_FOUND"
//	@Failure     409       {object} response.Error "INVALID_STATE (the user is banned)"
//	@Router      /channels/{channelId}/timeouts/{userId} [put]
func (r *routes) timeout(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId", "userId")
	if !ok {
		return nil
	}

	var body request.Timeout
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	if errs := body.Validate(); len(errs) > 0 {
		return validationFailed(ctx, errs)
	}

	x, err := r.mod.Timeout(ctx.UserContext(), principal(ctx), p[0], p[1], time.Duration(*body.DurationSec)*time.Second, body.Reason)
	if err != nil {
		return r.fail(ctx, "timeout", err)
	}

	return ctx.JSON(response.Data[response.Restriction]{Data: restrictionOf(x)})
}

// untimeout godoc
//
//	@Summary     Lift a timeout
//	@Description Viewers get `user_restored`. Nothing to lift is a no-op.
//	@Tags        moderation
//	@Security    BearerAuth
//	@Param       channelId path string true "channel id"
//	@Param       userId    path string true "user id"
//	@Success     204
//	@Failure     403 {object} response.Error "FORBIDDEN"
//	@Router      /channels/{channelId}/timeouts/{userId} [delete]
func (r *routes) untimeout(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId", "userId")
	if !ok {
		return nil
	}

	if err := r.mod.Untimeout(ctx.UserContext(), principal(ctx), p[0], p[1]); err != nil {
		return r.fail(ctx, "untimeout", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// ban godoc
//
//	@Summary     Ban a user from the channel's chat
//	@Description Like a timeout without end (CHAT_BANNED), for every stream of the channel until unbanned.
//	@Tags        moderation
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string      true "channel id"
//	@Param       userId    path     string      true "user id"
//	@Param       body      body     request.Ban false "reason"
//	@Success     200       {object} response.Data[response.Restriction]
//	@Failure     400       {object} response.Error "VALIDATION_FAILED (reason = LENGTH, userId = INVALID)"
//	@Failure     403       {object} response.Error "FORBIDDEN (details.target = PROTECTED)"
//	@Failure     404       {object} response.Error "NOT_FOUND"
//	@Router      /channels/{channelId}/bans/{userId} [put]
func (r *routes) ban(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId", "userId")
	if !ok {
		return nil
	}

	var body request.Ban
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(&body); err != nil {
			return validationFailed(ctx, nil)
		}
	}

	x, err := r.mod.Ban(ctx.UserContext(), principal(ctx), p[0], p[1], body.Reason)
	if err != nil {
		return r.fail(ctx, "ban", err)
	}

	return ctx.JSON(response.Data[response.Restriction]{Data: restrictionOf(x)})
}

// unban godoc
//
//	@Summary  Lift a ban
//	@Tags     moderation
//	@Security BearerAuth
//	@Param    channelId path string true "channel id"
//	@Param    userId    path string true "user id"
//	@Success  204
//	@Failure  403 {object} response.Error "FORBIDDEN"
//	@Router   /channels/{channelId}/bans/{userId} [delete]
func (r *routes) unban(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId", "userId")
	if !ok {
		return nil
	}

	if err := r.mod.Unban(ctx.UserContext(), principal(ctx), p[0], p[1]); err != nil {
		return r.fail(ctx, "unban", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// restrictions godoc
//
//	@Summary  Bans and running timeouts of the channel
//	@Tags     moderation
//	@Produce  json
//	@Security BearerAuth
//	@Param    channelId path     string true  "channel id"
//	@Param    type      query    string false "BAN | TIMEOUT"
//	@Param    cursor    query    string false "nextCursor of the previous page"
//	@Param    limit     query    int    false "1–50" default(24)
//	@Success  200       {object} response.Data[response.RestrictionPage]
//	@Failure  400       {object} response.Error "VALIDATION_FAILED (type = ENUM, cursor = INVALID)"
//	@Failure  403       {object} response.Error "FORBIDDEN"
//	@Router   /channels/{channelId}/restrictions [get]
func (r *routes) restrictions(ctx *fiber.Ctx) error {
	channelID, q, ok := pageQuery(ctx)
	if !ok {
		return nil
	}

	var t *entity.RestrictionType

	if q.Type != "" {
		rt := entity.RestrictionType(q.Type)
		t = &rt
	}

	page, err := r.mod.Restrictions(ctx.UserContext(), principal(ctx), channelID, t, q.Cursor, q.Limit)
	if err != nil {
		return r.fail(ctx, "restrictions", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	out := response.RestrictionPage{Items: mapItems(page.Items, restrictionOf), NextCursor: page.NextCursor}

	return ctx.JSON(response.Data[response.RestrictionPage]{Data: out})
}

// card godoc
//
//	@Summary     A user seen from the channel's chat
//	@Description Badges, channel role, follow date; restriction only for the owner, moderators and admins.
//	@Tags        moderation
//	@Produce     json
//	@Param       channelId path     string true "channel id"
//	@Param       userId    path     string true "user id"
//	@Success     200       {object} response.Data[response.UserCard]
//	@Failure     404       {object} response.Error "NOT_FOUND"
//	@Router      /channels/{channelId}/users/{userId}/card [get]
func (r *routes) card(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId", "userId")
	if !ok {
		return nil
	}

	c, err := r.mod.Card(ctx.UserContext(), principal(ctx), p[0], p[1])
	if err != nil {
		return r.fail(ctx, "card", err)
	}

	out := response.UserCard{User: userRefOf(c.User), CreatedAt: c.CreatedAt, Badges: c.Badges, FollowedAt: c.FollowedAt}
	if c.Role != "" {
		out.Role = &c.Role
	}

	if out.Badges == nil {
		out.Badges = []string{}
	}

	if c.Restriction != nil {
		out.Restriction = &response.RestrictionState{Type: string(c.Restriction.Type), Until: c.Restriction.Until}
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.UserCard]{Data: out})
}

// terms godoc
//
//	@Summary  Blocked terms of the channel
//	@Tags     moderation
//	@Produce  json
//	@Security BearerAuth
//	@Param    channelId path     string true "channel id"
//	@Success  200       {object} response.Data[response.BlockedTermList]
//	@Failure  403       {object} response.Error "FORBIDDEN"
//	@Router   /channels/{channelId}/blocked-terms [get]
func (r *routes) terms(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId")
	if !ok {
		return nil
	}

	list, err := r.mod.Terms(ctx.UserContext(), principal(ctx), p[0])
	if err != nil {
		return r.fail(ctx, "terms", err)
	}

	out := response.BlockedTermList{Items: make([]response.BlockedTerm, 0, len(list)), Limit: entity.BlockedTermsMax}
	for _, t := range list {
		out.Items = append(out.Items, termOf(t))
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.BlockedTermList]{Data: out})
}

// addTerm godoc
//
//	@Summary     Block a term in the channel's chat
//	@Description Matches whole words, ignoring case and diacritics ("spam" blocks "SPẢM", not "spammer"). A term already blocked in another spelling answers 200 with it.
//	@Tags        moderation
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string              true "channel id"
//	@Param       body      body     request.BlockedTerm true "term"
//	@Success     201       {object} response.Data[response.BlockedTerm]
//	@Success     200       {object} response.Data[response.BlockedTerm] "already blocked"
//	@Failure     400       {object} response.Error "VALIDATION_FAILED (term = REQUIRED | LENGTH)"
//	@Failure     403       {object} response.Error "FORBIDDEN"
//	@Failure     409       {object} response.Error "CONFLICT (details.limit = 100)"
//	@Router      /channels/{channelId}/blocked-terms [post]
func (r *routes) addTerm(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId")
	if !ok {
		return nil
	}

	var body request.BlockedTerm
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	t, created, err := r.mod.AddTerm(ctx.UserContext(), principal(ctx), p[0], body.Term)
	if err != nil {
		return r.fail(ctx, "addTerm", err)
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	return ctx.Status(status).JSON(response.Data[response.BlockedTerm]{Data: termOf(t)})
}

// removeTerm godoc
//
//	@Summary  Unblock a term
//	@Tags     moderation
//	@Security BearerAuth
//	@Param    channelId path string true "channel id"
//	@Param    termId    path string true "term id"
//	@Success  204
//	@Failure  403 {object} response.Error "FORBIDDEN"
//	@Router   /channels/{channelId}/blocked-terms/{termId} [delete]
func (r *routes) removeTerm(ctx *fiber.Ctx) error {
	p, ok := ids(ctx, "channelId")
	if !ok {
		return nil
	}

	if err := r.mod.RemoveTerm(ctx.UserContext(), principal(ctx), p[0], ctx.Params("termId")); err != nil {
		return r.fail(ctx, "removeTerm", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// mapItems maps a page's items to their DTOs (never null in JSON).
func mapItems[T, R any](in []T, f func(T) R) []R {
	out := make([]R, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}

	return out
}

func userRefOf(u entity.UserRef) response.UserRef {
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}

	return response.UserRef{ID: u.ID, Username: u.Username, DisplayName: name}
}

func restrictionOf(x entity.ActiveRestriction) response.Restriction {
	out := response.Restriction{
		User: userRefOf(x.User), Type: string(x.Type), Actor: response.ActorRef{ID: x.Actor.ID, Username: x.Actor.Username},
		Reason: x.Reason, CreatedAt: x.CreatedAt.UTC(),
	}

	if x.Until != nil {
		t := x.Until.UTC()
		out.Until = &t
	}

	return out
}

func termOf(t entity.BlockedTerm) response.BlockedTerm {
	return response.BlockedTerm{
		ID: t.ID, Term: t.Display, CreatedBy: response.ActorRef{ID: t.CreatedBy.ID, Username: t.CreatedBy.Username},
		CreatedAt: t.CreatedAt.UTC(),
	}
}
