package v1

import (
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/channels"
)

// profile godoc
//
//	@Summary     Public channel page by handle
//	@Description The handle is case-insensitive (3–25 characters of [A-Za-z0-9_]); anything else is 404. currentStream is set while the channel is live; viewer is null for guests.
//	@Tags        channels
//	@Produce     json
//	@Security    BearerAuth
//	@Param       handle path     string true "channel handle"
//	@Success     200    {object} response.Data[response.ChannelProfile]
//	@Failure     401    {object} response.Error "UNAUTHORIZED (token sent but invalid)"
//	@Failure     403    {object} response.Error "CHANNEL_SUSPENDED"
//	@Failure     404    {object} response.Error "NOT_FOUND"
//	@Router      /channels/{handle} [get]
func (r *routes) profile(ctx *fiber.Ctx) error {
	p, err := r.uc.Directory.Profile(ctx.UserContext(), ctx.Params("handle"), caller(ctx))
	if err != nil {
		return r.fail(ctx, "profile", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(response.Data[response.ChannelProfile]{Data: response.ProfileOf(p)})
}

// follow godoc
//
//	@Summary     Follow a channel
//	@Description Idempotent: following again returns the existing follow (notifyLevel ALL for a new one).
//	@Tags        follows
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "channel id (uuid)"
//	@Success     200 {object} response.Data[response.FollowState]
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Failure     403 {object} response.Error "CHANNEL_SUSPENDED"
//	@Failure     404 {object} response.Error "NOT_FOUND"
//	@Failure     422 {object} response.Error "CANNOT_FOLLOW_SELF"
//	@Router      /channels/{id}/follow [post]
func (r *routes) follow(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	channelID, ok := idParam(ctx)
	if !ok {
		return notFound(ctx)
	}

	f, err := r.uc.Follows.Follow(ctx.UserContext(), userID, channelID)
	if err != nil {
		return r.fail(ctx, "follow", err)
	}

	return ctx.JSON(response.Data[response.FollowState]{Data: response.FollowStateOf(f)})
}

// setNotifyLevel godoc
//
//	@Summary Change the bell of a followed channel
//	@Tags    follows
//	@Accept  json
//	@Produce json
//	@Security BearerAuth
//	@Param   id   path     string              true "channel id (uuid)"
//	@Param   body body     request.NotifyLevel true "notification level"
//	@Success 200  {object} response.Data[response.FollowState]
//	@Failure 400  {object} response.Error "VALIDATION_FAILED (notifyLevel = ENUM)"
//	@Failure 401  {object} response.Error "UNAUTHORIZED"
//	@Failure 404  {object} response.Error "NOT_FOUND (unknown channel or not following)"
//	@Router  /channels/{id}/follow [put]
func (r *routes) setNotifyLevel(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	channelID, ok := idParam(ctx)
	if !ok {
		return notFound(ctx)
	}

	var body request.NotifyLevel
	if err = ctx.BodyParser(&body); err != nil {
		return invalid(ctx, map[string]string{_fieldBody: entity.DetailFormat})
	}

	if errs := body.Validate(); len(errs) > 0 {
		return invalid(ctx, errs)
	}

	f, err := r.uc.Follows.SetNotifyLevel(ctx.UserContext(), userID, channelID, entity.NotifyLevel(body.NotifyLevel))
	if err != nil {
		return r.fail(ctx, "setNotifyLevel", err)
	}

	return ctx.JSON(response.Data[response.FollowState]{Data: response.FollowStateOf(f)})
}

// unfollow godoc
//
//	@Summary  Unfollow a channel (always 204)
//	@Tags     follows
//	@Security BearerAuth
//	@Param    id path string true "channel id (uuid)"
//	@Success  204
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Router   /channels/{id}/follow [delete]
func (r *routes) unfollow(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	channelID, ok := idParam(ctx)
	if !ok {
		return ctx.SendStatus(http.StatusNoContent)
	}

	if err = r.uc.Follows.Unfollow(ctx.UserContext(), userID, channelID); err != nil {
		return r.fail(ctx, "unfollow", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// channelSchedule godoc
//
//	@Summary     A channel's upcoming broadcasts (Schedule tab)
//	@Description Occurrences in [from, to), ascending; default [now, now + 14 days]; to − from ≤ 31 days and to ≤ now + 90 days. reminded is null for guests.
//	@Tags        schedules
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string true  "channel id (uuid)"
//	@Param       from query    string false "RFC 3339"
//	@Param       to   query    string false "RFC 3339"
//	@Success     200  {object} response.Data[response.OccurrenceList]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (from / to = FORMAT | MAX)"
//	@Failure     404  {object} response.Error "NOT_FOUND"
//	@Router      /channels/{id}/schedule [get]
func (r *routes) channelSchedule(ctx *fiber.Ctx) error {
	channelID, ok := idParam(ctx)
	if !ok {
		return notFound(ctx)
	}

	from, to, errs := r.scheduleRange(ctx.Query("from"), ctx.Query("to"), time.Now().UTC())
	if len(errs) > 0 {
		return invalid(ctx, errs)
	}

	items, err := r.uc.Directory.ChannelSchedule(ctx.UserContext(), channelID, caller(ctx), from, to)
	if err != nil {
		return r.fail(ctx, "channelSchedule", err)
	}

	out := response.OccurrenceList{Items: make([]response.Occurrence, 0, len(items))}
	for _, o := range items {
		out.Items = append(out.Items, response.OccurrenceOf(o))
	}

	return ctx.JSON(response.Data[response.OccurrenceList]{Data: out})
}

func (r *routes) scheduleRange(fromQ, toQ string, now time.Time) (from, to time.Time, errs map[string]string) {
	from, to = now, now.Add(r.o.ScheduleWindow)
	errs = map[string]string{}

	if fromQ != "" {
		t, d := request.ParseInstant(fromQ)
		if d != "" {
			errs["from"] = d
		}

		from = t
	}

	if toQ != "" {
		t, d := request.ParseInstant(toQ)
		if d != "" {
			errs["to"] = d
		}

		to = t
	} else if fromQ != "" {
		to = from.Add(r.o.ScheduleWindow)
	}

	if len(errs) == 0 && (!to.After(from) || to.Sub(from) > entity.ScheduleRangeMax || to.After(now.Add(r.o.ScheduleHorizon))) {
		errs["to"] = entity.DetailMax
	}

	return from, to, errs
}

// following godoc
//
//	@Summary     Channels the caller follows
//	@Description status=live is sorted by viewers (no paging, at most 200); all / offline list live channels first, then by last broadcast, with a cursor.
//	@Tags        follows
//	@Produce     json
//	@Security    BearerAuth
//	@Param       status query    string false "filter" Enums(all,live,offline) default(all)
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "1–50" default(24)
//	@Success     200    {object} response.Data[response.FollowingList]
//	@Failure     400    {object} response.Error "VALIDATION_FAILED (status = ENUM, cursor = INVALID)"
//	@Failure     401    {object} response.Error "UNAUTHORIZED"
//	@Router      /me/following [get]
func (r *routes) following(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	f := entity.FollowingFilter(ctx.Query("status", string(entity.FollowingAll)))
	if !f.Valid() {
		return invalid(ctx, map[string]string{"status": entity.DetailEnum})
	}

	page, err := r.uc.Follows.Following(ctx.UserContext(), userID, f, ctx.Query("cursor"), ctx.QueryInt("limit", channels.DefaultLimit))
	if err != nil {
		return r.fail(ctx, "following", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.FollowingList]{Data: response.FollowingList{
		Items: response.FollowedOf(page.Items), NextCursor: response.Cursor(page.NextCursor),
	}})
}

// sidebar godoc
//
//	@Summary     Followed channels for the shell's sidebar
//	@Description Every followed live channel (by viewers, up to limit), then offline ones to fill limit. Updates arrive on the WebSocket topic sidebar:{userId} (chat-svc).
//	@Tags        follows
//	@Produce     json
//	@Security    BearerAuth
//	@Param       limit query    int false "1–50" default(10)
//	@Success     200   {object} response.Data[response.Sidebar]
//	@Failure     401   {object} response.Error "UNAUTHORIZED"
//	@Router      /me/following/sidebar [get]
func (r *routes) sidebar(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	limit := ctx.QueryInt("limit", entity.SidebarFollowedMax)
	if limit < 1 || limit > entity.SidebarLimitMax {
		return invalid(ctx, map[string]string{"limit": entity.DetailMax})
	}

	sb, err := r.uc.Follows.Sidebar(ctx.UserContext(), userID, limit)
	if err != nil {
		return r.fail(ctx, "sidebar", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.Sidebar]{Data: response.Sidebar{
		Live: response.FollowedOf(sb.Live), Offline: response.FollowedOf(sb.Offline), TotalFollowing: sb.TotalFollowing,
	}})
}
