package v1

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

// lock -.
//
//	@Summary     Lock a user account
//	@Description Records the action and returns 202; identity applies it (status LOCKED, sessions revoked, sockets closed 4003) within seconds. durationHours 24, 168, 720 or null (until unlocked). Locking an admin → 403; oneself → 400 userId=INVALID. Locking a locked user sets the new end.
//	@Tags        admin-actions
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       userId path     string      true "user id"
//	@Param       body   body     LockRequest true "lock"
//	@Success     202    {object} Data[ActionAccepted]
//	@Failure     400    {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401    {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403    {object} ErrorResponse "FORBIDDEN"
//	@Failure     404    {object} ErrorResponse "NOT_FOUND"
//	@Router      /admin/users/{userId}/lock [post]
func (r *routes) lock(ctx *fiber.Ctx) error {
	adminID, err := caller(ctx)
	if err != nil {
		return err
	}

	var body LockRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	id, err := r.uc.Actions.LockUser(ctx.UserContext(), adminID, ctx.Params("userId"), usecase.LockInput{
		DurationHours: body.DurationHours, Reason: body.Reason, ReportID: body.ReportID,
	})

	return r.accepted(ctx, "lock", id, err)
}

func (r *routes) accepted(ctx *fiber.Ctx, op, id string, err error) error {
	if err != nil {
		return r.fail(ctx, op, err)
	}

	return ctx.Status(http.StatusAccepted).JSON(Data[ActionAccepted]{Data: ActionAccepted{ActionID: id}})
}

// action runs one of the reason-only admin actions on the :param target.
func (r *routes) action(ctx *fiber.Ctx, op, param string,
	run func(ctx *fiber.Ctx, adminID, target string, in usecase.ActionInput) (string, error),
) error {
	adminID, err := caller(ctx)
	if err != nil {
		return err
	}

	var body ActionRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	id, err := run(ctx, adminID, ctx.Params(param), usecase.ActionInput{Reason: body.Reason, ReportID: body.ReportID})

	return r.accepted(ctx, op, id, err)
}

// unlock -.
//
//	@Summary     Unlock a user account
//	@Description Always 202; identity ignores a user who is not locked.
//	@Tags        admin-actions
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       userId path     string        true "user id"
//	@Param       body   body     ActionRequest true "reason (reportId ignored)"
//	@Success     202    {object} Data[ActionAccepted]
//	@Failure     400    {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401    {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403    {object} ErrorResponse "FORBIDDEN"
//	@Failure     404    {object} ErrorResponse "NOT_FOUND"
//	@Router      /admin/users/{userId}/unlock [post]
func (r *routes) unlock(ctx *fiber.Ctx) error {
	return r.action(ctx, "unlock", "userId", func(c *fiber.Ctx, a, t string, in usecase.ActionInput) (string, error) {
		in.ReportID = nil

		return r.uc.Actions.UnlockUser(c.UserContext(), a, t, in)
	})
}

// suspend -.
//
//	@Summary     Suspend a channel
//	@Description 202; channel-svc suspends it, stream-svc ends its broadcast and cancels its schedules, chat closes its rooms.
//	@Tags        admin-actions
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string        true "channel id"
//	@Param       body      body     ActionRequest true "reason"
//	@Success     202       {object} Data[ActionAccepted]
//	@Failure     400       {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401       {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403       {object} ErrorResponse "FORBIDDEN"
//	@Failure     404       {object} ErrorResponse "NOT_FOUND"
//	@Router      /admin/channels/{channelId}/suspend [post]
func (r *routes) suspend(ctx *fiber.Ctx) error {
	return r.action(ctx, "suspend", "channelId", func(c *fiber.Ctx, a, t string, in usecase.ActionInput) (string, error) {
		return r.uc.Actions.SuspendChannel(c.UserContext(), a, t, in)
	})
}

// restore -.
//
//	@Summary     Restore a suspended channel
//	@Tags        admin-actions
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string        true "channel id"
//	@Param       body      body     ActionRequest true "reason (reportId ignored)"
//	@Success     202       {object} Data[ActionAccepted]
//	@Failure     400       {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401       {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403       {object} ErrorResponse "FORBIDDEN"
//	@Failure     404       {object} ErrorResponse "NOT_FOUND"
//	@Router      /admin/channels/{channelId}/restore [post]
func (r *routes) restore(ctx *fiber.Ctx) error {
	return r.action(ctx, "restore", "channelId", func(c *fiber.Ctx, a, t string, in usecase.ActionInput) (string, error) {
		in.ReportID = nil

		return r.uc.Actions.RestoreChannel(c.UserContext(), a, t, in)
	})
}

// takedown -.
//
//	@Summary     Take a stream down
//	@Description A stream on air ends with endReason admin_takedown; an ended stream loses its VOD. SCHEDULED, CANCELLED, or ENDED without a VOD → 409 INVALID_STATE.
//	@Tags        admin-actions
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       streamId path     string        true "stream id"
//	@Param       body     body     ActionRequest true "reason"
//	@Success     202      {object} Data[ActionAccepted]
//	@Failure     400      {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401      {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403      {object} ErrorResponse "FORBIDDEN"
//	@Failure     404      {object} ErrorResponse "NOT_FOUND"
//	@Failure     409      {object} ErrorResponse "INVALID_STATE"
//	@Router      /admin/streams/{streamId}/takedown [post]
func (r *routes) takedown(ctx *fiber.Ctx) error {
	return r.action(ctx, "takedown", "streamId", func(c *fiber.Ctx, a, t string, in usecase.ActionInput) (string, error) {
		return r.uc.Actions.TakedownStream(c.UserContext(), a, t, in)
	})
}

func (r *routes) directoryQuery(ctx *fiber.Ctx) (usecase.DirectoryQuery, bool) {
	cur, limit, ok := pageQuery(ctx)

	return usecase.DirectoryQuery{Q: ctx.Query("q"), Status: optional(ctx, "status"), Cursor: cur, Limit: limit}, ok
}

// users -.
//
//	@Summary     Look up users
//	@Description q: a UUID matches the id, anything else a username prefix (case-insensitive). Sorted by username. status is the effective one (an expired lock reads ACTIVE).
//	@Tags        admin-lookup
//	@Produce     json
//	@Security    BearerAuth
//	@Param       q      query    string false "1–50 characters"
//	@Param       status query    string false "ACTIVE, LOCKED or DELETED"
//	@Param       cursor query    string false "nextCursor"
//	@Param       limit  query    int    false "1–100, default 20"
//	@Success     200    {object} Data[UserList]
//	@Failure     400    {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401    {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403    {object} ErrorResponse "FORBIDDEN"
//	@Router      /admin/users [get]
func (r *routes) users(ctx *fiber.Ctx) error {
	q, ok := r.directoryQuery(ctx)
	if !ok {
		return validationFailed(ctx, map[string]string{"limit": _detailFormat})
	}

	p, err := r.uc.Directory.Users(ctx.UserContext(), q)
	if err != nil {
		return r.fail(ctx, "users", err)
	}

	return ctx.JSON(Data[UserList]{Data: UserList{Items: mapItems(p.Items, userRow), NextCursor: cursor(p.Next)}})
}

// channels -.
//
//	@Summary     Look up channels
//	@Description q: a UUID matches the id, anything else a handle prefix or a display-name substring. Sorted by handle.
//	@Tags        admin-lookup
//	@Produce     json
//	@Security    BearerAuth
//	@Param       q      query    string false "1–50 characters"
//	@Param       status query    string false "ACTIVE or SUSPENDED"
//	@Param       cursor query    string false "nextCursor"
//	@Param       limit  query    int    false "1–100, default 20"
//	@Success     200    {object} Data[ChannelList]
//	@Failure     400    {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401    {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403    {object} ErrorResponse "FORBIDDEN"
//	@Router      /admin/channels [get]
func (r *routes) channels(ctx *fiber.Ctx) error {
	q, ok := r.directoryQuery(ctx)
	if !ok {
		return validationFailed(ctx, map[string]string{"limit": _detailFormat})
	}

	p, err := r.uc.Directory.Channels(ctx.UserContext(), q)
	if err != nil {
		return r.fail(ctx, "channels", err)
	}

	return ctx.JSON(Data[ChannelList]{Data: ChannelList{Items: mapItems(p.Items, adminChannel), NextCursor: cursor(p.Next)}})
}

// channel -.
//
//	@Summary     Channel detail (suspended channels included)
//	@Tags        admin-lookup
//	@Produce     json
//	@Security    BearerAuth
//	@Param       channelId path     string true "channel id"
//	@Success     200       {object} Data[AdminChannel]
//	@Failure     401       {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403       {object} ErrorResponse "FORBIDDEN"
//	@Failure     404       {object} ErrorResponse "NOT_FOUND"
//	@Router      /admin/channels/{channelId} [get]
func (r *routes) channel(ctx *fiber.Ctx) error {
	c, err := r.uc.Directory.Channel(ctx.UserContext(), ctx.Params("channelId"))
	if err != nil {
		return r.fail(ctx, "channel", err)
	}

	return ctx.JSON(Data[AdminChannel]{Data: adminChannel(c)})
}

// auditLog -.
//
//	@Summary     Audit log
//	@Description Newest first. Rows written by safety-svc and mapped from chat, identity and channel-role events; append-only.
//	@Tags        admin-audit
//	@Produce     json
//	@Security    BearerAuth
//	@Param       actorId      query    string false "user id"
//	@Param       actor        query    string false "username (leading @ optional), used when actorId is absent; unknown → empty page"
//	@Param       action       query    string false "e.g. USER_LOCKED, CHAT_BAN"
//	@Param       resourceType query    string false "USER, CHANNEL, STREAM, MESSAGE, REPORT, CREATOR_APPLICATION"
//	@Param       resourceId   query    string false "uuid"
//	@Param       from         query    string false "RFC 3339, inclusive"
//	@Param       to           query    string false "RFC 3339, exclusive"
//	@Param       cursor       query    string false "nextCursor"
//	@Param       limit        query    int    false "1–100, default 20"
//	@Success     200          {object} Data[AuditList]
//	@Failure     400          {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401          {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403          {object} ErrorResponse "FORBIDDEN"
//	@Router      /admin/audit-log [get]
func (r *routes) auditLog(ctx *fiber.Ctx) error {
	cur, limit, ok := pageQuery(ctx)
	if !ok {
		return validationFailed(ctx, map[string]string{"limit": _detailFormat})
	}

	from, okFrom := optionalTime(ctx, "from")
	to, okTo := optionalTime(ctx, "to")

	if !okFrom || !okTo {
		details := map[string]string{}
		if !okFrom {
			details["from"] = _detailFormat
		}

		if !okTo {
			details["to"] = _detailFormat
		}

		return validationFailed(ctx, details)
	}

	p, err := r.uc.Audit.List(ctx.UserContext(), usecase.AuditQuery{
		AuditFilter: repo.AuditFilter{
			ActorID: optional(ctx, "actorId"), Action: optional(ctx, "action"), ResourceType: optional(ctx, "resourceType"),
			ResourceID: optional(ctx, "resourceId"), From: from, To: to,
		},
		Actor:  ctx.Query("actor"),
		Cursor: cur, Limit: limit,
	})
	if err != nil {
		return r.fail(ctx, "auditLog", err)
	}

	return ctx.JSON(Data[AuditList]{Data: AuditList{Items: mapItems(p.Items, auditEntry), NextCursor: cursor(p.Next)}})
}
