package v1

import (
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
)

func (r *routes) studioBody(ctx *fiber.Ctx, ch entity.Channel) error {
	return ctx.JSON(response.Data[response.StudioChannel]{Data: response.StudioChannelOf(ch, r.uc.Studio.NextHandleChangeAt(ch))})
}

// studioChannel godoc
//
//	@Summary  Studio: the caller's channel (profile form)
//	@Tags     studio
//	@Produce  json
//	@Security BearerAuth
//	@Success  200 {object} response.Data[response.StudioChannel]
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Failure  403 {object} response.Error "NOT_CREATOR"
//	@Router   /studio/channel [get]
func (r *routes) studioChannel(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	ch, err := r.uc.Studio.Channel(ctx.UserContext(), userID)
	if err != nil {
		return r.fail(ctx, "studioChannel", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return r.studioBody(ctx, ch)
}

// updateChannel godoc
//
//	@Summary     Studio: edit the channel profile
//	@Description At least one field. displayName 1–50, bio ≤ 300 (null clears it), ≤ 5 https links (type YOUTUBE, X, FACEBOOK, TIKTOK, DISCORD, WEBSITE; url ≤ 300). handle ^[a-z0-9_]{3,25}$, not a reserved route word, once per 30 days: the old handle stops resolving at once.
//	@Tags        studio
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     request.ChannelPatch true "fields to change"
//	@Success     200  {object} response.Data[response.StudioChannel]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (body = REQUIRED, displayName / bio = LENGTH, socialLinks = MAX | FORMAT | ENUM, handle = FORMAT | INVALID)"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Failure     403  {object} response.Error "NOT_CREATOR"
//	@Failure     409  {object} response.Error "HANDLE_TAKEN (details.handle = TAKEN) or CONFLICT (details.nextChangeAt)"
//	@Router      /studio/channel [patch]
func (r *routes) updateChannel(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	body, ok := request.DecodeChannelPatch(ctx.Body())
	if !ok {
		return invalid(ctx, map[string]string{_fieldBody: entity.DetailFormat})
	}

	ch, err := r.uc.Studio.UpdateProfile(ctx.UserContext(), userID, body.Patch())
	if err != nil {
		return r.fail(ctx, "updateChannel", err)
	}

	return r.studioBody(ctx, ch)
}

// uploadAvatar godoc
//
//	@Summary     Studio: upload the channel avatar
//	@Description multipart/form-data with part "file": JPEG, PNG or WebP (checked by content, not name), ≤ 2 MB, at least 128×128. Stored as a 256×256 JPEG (center crop) under /media/.
//	@Tags        studio
//	@Accept      multipart/form-data
//	@Produce     json
//	@Security    BearerAuth
//	@Param       file formData file true "image"
//	@Success     200  {object} response.Data[response.ImageURL]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (file = REQUIRED | FORMAT | MIN)"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Failure     403  {object} response.Error "NOT_CREATOR"
//	@Failure     413  {object} response.Error "PAYLOAD_TOO_LARGE"
//	@Router      /studio/channel/avatar [put]
func (r *routes) uploadAvatar(ctx *fiber.Ctx) error { return r.upload(ctx, entity.ImageAvatar) }

// uploadBanner godoc
//
//	@Summary     Studio: upload the channel banner
//	@Description Same as the avatar; ≤ 5 MB, at least 800×250, stored as a 1600×500 JPEG (center crop to 16:5).
//	@Tags        studio
//	@Accept      multipart/form-data
//	@Produce     json
//	@Security    BearerAuth
//	@Param       file formData file true "image"
//	@Success     200  {object} response.Data[response.ImageURL]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (file = REQUIRED | FORMAT | MIN)"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Failure     403  {object} response.Error "NOT_CREATOR"
//	@Failure     413  {object} response.Error "PAYLOAD_TOO_LARGE"
//	@Router      /studio/channel/banner [put]
func (r *routes) uploadBanner(ctx *fiber.Ctx) error { return r.upload(ctx, entity.ImageBanner) }

func (r *routes) upload(ctx *fiber.Ctx, kind entity.ImageKind) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	maxBytes := r.o.AvatarMaxBytes
	if kind == entity.ImageBanner {
		maxBytes = r.o.BannerMaxBytes
	}

	fh, err := ctx.FormFile("file")
	if err != nil {
		return invalid(ctx, map[string]string{"file": entity.DetailRequired})
	}

	if fh.Size > int64(maxBytes) {
		return r.fail(ctx, "upload", entity.ErrImageTooLarge)
	}

	f, err := fh.Open()
	if err != nil {
		return r.fail(ctx, "upload - open", err)
	}
	defer f.Close()

	// One byte more than allowed tells "too large" apart from "exactly the limit".
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return r.fail(ctx, "upload - read", err)
	}

	if len(data) > maxBytes {
		return r.fail(ctx, "upload", entity.ErrImageTooLarge)
	}

	u, err := r.uc.Studio.SetImage(ctx.UserContext(), userID, kind, data)
	if err != nil {
		return r.fail(ctx, "upload", err)
	}

	return ctx.JSON(response.Data[response.ImageURL]{Data: response.ImageURL{URL: u}})
}

// stats godoc
//
//	@Summary  Studio dashboard: followers and the next broadcast
//	@Tags     studio
//	@Produce  json
//	@Security BearerAuth
//	@Param    range query    string false "period" Enums(7d,28d) default(7d)
//	@Success  200   {object} response.Data[response.ChannelStats]
//	@Failure  400   {object} response.Error "VALIDATION_FAILED (range = ENUM)"
//	@Failure  401   {object} response.Error "UNAUTHORIZED"
//	@Failure  403   {object} response.Error "NOT_CREATOR"
//	@Router   /studio/channel/stats [get]
func (r *routes) stats(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	st, err := r.uc.Studio.Stats(ctx.UserContext(), userID, entity.StatsRange(ctx.Query("range", string(entity.Range7d))))
	if err != nil {
		return r.fail(ctx, "stats", err)
	}

	out := response.ChannelStats{Range: string(st.Range), FollowerCount: st.FollowerCount, NewFollowers: st.NewFollowers}
	if st.Upcoming != nil {
		out.Upcoming = &response.Upcoming{
			ScheduleID: st.Upcoming.ScheduleID, OccurrenceAt: st.Upcoming.OccurrenceAt.UTC(),
			Title: st.Upcoming.Title, ReminderCount: st.Upcoming.ReminderCount,
		}
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.ChannelStats]{Data: out})
}

// schedules godoc
//
//	@Summary     Studio: the caller's schedules
//	@Description ACTIVE lists schedules with an occurrence still to come; nextOccurrences holds up to 5.
//	@Tags        studio
//	@Produce     json
//	@Security    BearerAuth
//	@Param       status query    string false "status" Enums(ACTIVE,CANCELLED) default(ACTIVE)
//	@Success     200    {object} response.Data[response.ScheduleList]
//	@Failure     400    {object} response.Error "VALIDATION_FAILED (status = ENUM)"
//	@Failure     401    {object} response.Error "UNAUTHORIZED"
//	@Failure     403    {object} response.Error "NOT_CREATOR"
//	@Router      /studio/schedules [get]
func (r *routes) schedules(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	st := entity.ScheduleStatus(ctx.Query("status", string(entity.ScheduleActive)))
	if !st.Valid() {
		return invalid(ctx, map[string]string{"status": entity.DetailEnum})
	}

	list, err := r.uc.Schedules.List(ctx.UserContext(), userID, st)
	if err != nil {
		return r.fail(ctx, "schedules", err)
	}

	out := response.ScheduleList{Items: make([]response.Schedule, 0, len(list))}
	for _, v := range list {
		out.Items = append(out.Items, response.ScheduleOf(v))
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.ScheduleList]{Data: out})
}

func scheduleInput(ctx *fiber.Ctx) (in entity.ScheduleInput, errs map[string]string) {
	var body request.Schedule
	if err := ctx.BodyParser(&body); err != nil {
		return entity.ScheduleInput{}, map[string]string{_fieldBody: entity.DetailFormat}
	}

	return body.Input()
}

// createSchedule godoc
//
//	@Summary     Studio: create a schedule
//	@Description title 1–140; startAt in the future and ≤ 90 days ahead; durationMin 15–720; rrule null (one-off) or FREQ=WEEKLY;BYDAY=… (must include startAt's weekday in timezone) with optional COUNT=1–100 or UNTIL; at most 20 active schedules. Overlaps are allowed.
//	@Tags        studio
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     request.Schedule true "schedule"
//	@Success     201  {object} response.Data[response.Schedule]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (title = LENGTH, startAt = REQUIRED | FORMAT | MIN | MAX, durationMin = MIN | MAX, rrule / timezone = FORMAT, categoryId = INVALID)"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Failure     403  {object} response.Error "NOT_CREATOR"
//	@Failure     409  {object} response.Error "CONFLICT (details.limit)"
//	@Router      /studio/schedules [post]
func (r *routes) createSchedule(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	in, errs := scheduleInput(ctx)
	if len(errs) > 0 {
		return invalid(ctx, errs)
	}

	v, err := r.uc.Schedules.Create(ctx.UserContext(), userID, in)
	if err != nil {
		return r.fail(ctx, "createSchedule", err)
	}

	return ctx.Status(http.StatusCreated).JSON(response.Data[response.Schedule]{Data: response.ScheduleOf(v)})
}

// updateSchedule godoc
//
//	@Summary     Studio: edit a schedule
//	@Description Same rules as create (startAt may stay in the past when unchanged). Changing the start, rule or time zone cancels the announced and reminded occurrences of the old series.
//	@Tags        studio
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string           true "schedule id (uuid)"
//	@Param       body body     request.Schedule true "schedule"
//	@Success     200  {object} response.Data[response.Schedule]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Failure     403  {object} response.Error "NOT_CREATOR"
//	@Failure     404  {object} response.Error "NOT_FOUND"
//	@Failure     409  {object} response.Error "INVALID_STATE (cancelled)"
//	@Router      /studio/schedules/{id} [put]
func (r *routes) updateSchedule(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	id, ok := idParam(ctx)
	if !ok {
		return notFound(ctx)
	}

	in, errs := scheduleInput(ctx)
	if len(errs) > 0 {
		return invalid(ctx, errs)
	}

	v, err := r.uc.Schedules.Update(ctx.UserContext(), userID, id, in)
	if err != nil {
		return r.fail(ctx, "updateSchedule", err)
	}

	return ctx.JSON(response.Data[response.Schedule]{Data: response.ScheduleOf(v)})
}

// cancelSchedule godoc
//
//	@Summary  Studio: cancel a schedule and its future occurrences
//	@Tags     studio
//	@Security BearerAuth
//	@Param    id path string true "schedule id (uuid)"
//	@Success  204
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Failure  403 {object} response.Error "NOT_CREATOR"
//	@Failure  404 {object} response.Error "NOT_FOUND"
//	@Failure  409 {object} response.Error "INVALID_STATE (already cancelled)"
//	@Router   /studio/schedules/{id} [delete]
func (r *routes) cancelSchedule(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	id, ok := idParam(ctx)
	if !ok {
		return notFound(ctx)
	}

	if err = r.uc.Schedules.Cancel(ctx.UserContext(), userID, id); err != nil {
		return r.fail(ctx, "cancelSchedule", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// occurrenceParams reads {id} and {occurrenceAt} (RFC 3339, URL-encoded); ok is false when a 404 / 400 was sent.
func occurrenceParams(ctx *fiber.Ctx) (id string, at time.Time, ok bool, err error) {
	id, ok = idParam(ctx)
	if !ok {
		return "", time.Time{}, false, notFound(ctx)
	}

	raw, uerr := url.PathUnescape(ctx.Params("occurrenceAt"))
	if uerr != nil {
		return "", time.Time{}, false, notFound(ctx)
	}

	at, d := request.ParseInstant(raw)
	if d != "" {
		return "", time.Time{}, false, notFound(ctx)
	}

	return id, at, true, nil
}

// moveOccurrence godoc
//
//	@Summary     Studio: move one occurrence
//	@Description The occurrence becomes a one-off schedule (parentId = this schedule) at startAt; its reminders follow it.
//	@Tags        studio
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id           path     string                 true "schedule id (uuid)"
//	@Param       occurrenceAt path     string                 true "RFC 3339, URL-encoded"
//	@Param       body         body     request.MoveOccurrence true "new start"
//	@Success     200          {object} response.Data[response.MovedOccurrence]
//	@Failure     400          {object} response.Error "VALIDATION_FAILED (startAt = REQUIRED | FORMAT | MIN | MAX)"
//	@Failure     401          {object} response.Error "UNAUTHORIZED"
//	@Failure     403          {object} response.Error "NOT_CREATOR"
//	@Failure     404          {object} response.Error "NOT_FOUND"
//	@Failure     409          {object} response.Error "INVALID_STATE (past or on air)"
//	@Router      /studio/schedules/{id}/occurrences/{occurrenceAt} [put]
func (r *routes) moveOccurrence(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	id, occ, ok, err := occurrenceParams(ctx)
	if !ok {
		return err
	}

	var body request.MoveOccurrence
	if err = ctx.BodyParser(&body); err != nil {
		return invalid(ctx, map[string]string{_fieldBody: entity.DetailFormat})
	}

	start, d := request.ParseInstant(body.StartAt)
	if d != "" {
		return invalid(ctx, map[string]string{"startAt": d})
	}

	v, err := r.uc.Schedules.MoveOccurrence(ctx.UserContext(), userID, id, occ, start)
	if err != nil {
		return r.fail(ctx, "moveOccurrence", err)
	}

	return ctx.JSON(response.Data[response.MovedOccurrence]{Data: response.MovedOccurrence{Schedule: response.ScheduleOf(v)}})
}

// cancelOccurrence godoc
//
//	@Summary  Studio: cancel one occurrence
//	@Tags     studio
//	@Security BearerAuth
//	@Param    id           path string true "schedule id (uuid)"
//	@Param    occurrenceAt path string true "RFC 3339, URL-encoded"
//	@Success  204
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Failure  403 {object} response.Error "NOT_CREATOR"
//	@Failure  404 {object} response.Error "NOT_FOUND"
//	@Failure  409 {object} response.Error "INVALID_STATE (past or on air)"
//	@Router   /studio/schedules/{id}/occurrences/{occurrenceAt} [delete]
func (r *routes) cancelOccurrence(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	id, occ, ok, err := occurrenceParams(ctx)
	if !ok {
		return err
	}

	if err = r.uc.Schedules.CancelOccurrence(ctx.UserContext(), userID, id, occ); err != nil {
		return r.fail(ctx, "cancelOccurrence", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}
