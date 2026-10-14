package v1

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/streams"
)

// CodeInvalidState is returned when a VOD cannot change in its current state (EXPIRED).
const CodeInvalidState = "INVALID_STATE"

// dashboard godoc
//
//	@Summary     Studio dashboard: the broadcast on air, the last 5 broadcasts and counts over the range
//	@Description Counted on the tables directly. Followers and the next broadcast come from GET /v1/studio/channel/stats (channel-svc).
//	@Tags        studio
//	@Produce     json
//	@Security    BearerAuth
//	@Param       range query    string false "period" Enums(7d,28d) default(7d)
//	@Success     200   {object} Data[Dashboard]
//	@Failure     400   {object} ErrorResponse "VALIDATION_FAILED (range = ENUM)"
//	@Failure     401   {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403   {object} ErrorResponse "NOT_CREATOR"
//	@Router      /studio/dashboard [get]
func (r *routes) dashboard(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	d, err := r.studio.Dashboard(ctx.UserContext(), ch.ID, entity.Range(ctx.Query("range", string(entity.Range7d))))
	if errors.Is(err, entity.ErrInvalidState) {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid range", map[string]string{"range": _detailEnum})
	}

	if err != nil {
		return r.internal(ctx, "dashboard", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(Data[Dashboard]{Data: dashboardBody(d)})
}

func dashboardBody(d entity.Dashboard) Dashboard {
	out := Dashboard{RecentStreams: make([]RecentStream, 0, len(d.Recent))}

	if c := d.Current; c != nil {
		out.Current = &DashboardCurrent{
			StreamID: c.ID, Title: c.Title, Status: string(c.Status), StartedAt: utcPtr(c.StartedAt),
			ViewerCount: c.ViewerCount,
		}
	}

	for _, s := range d.Recent {
		rs := RecentStream{
			StreamID: s.StreamID, Title: s.Title, StartedAt: utcPtr(s.StartedAt), EndedAt: utcPtr(s.EndedAt),
			EndReason: s.EndReason, PeakViewers: s.PeakViewers, VODID: s.VODID, VODVisibility: s.VODVisibility,
		}
		if s.StartedAt != nil && s.EndedAt != nil {
			rs.DurationSec = int64(s.EndedAt.Sub(*s.StartedAt).Seconds())
		}

		out.RecentStreams = append(out.RecentStreams, rs)
	}

	out.Stats = DashboardStats{
		Range: string(d.Range), StreamCount: d.Stats.StreamCount, LiveSeconds: d.Stats.LiveSeconds,
		VODReadyPublic: d.Stats.VODReadyPublic, VODReadyHidden: d.Stats.VODReadyHidden,
	}

	return out
}

func (r *routes) studioVODBody(v entity.VOD) StudioVOD {
	return StudioVOD{
		ID: v.ID, StreamID: v.StreamID, Title: v.Title, DurationSec: v.DurationSec, ThumbnailURL: r.objectURL(v.ThumbnailURL),
		ManifestURL: r.objectURL(v.PlaylistPath), Status: v.Status, Visibility: v.Visibility, Category: categoryRef(v.CategoryID),
		CreatedAt: v.CreatedAt.UTC(), ExpiresAt: v.ExpiresAt.UTC(),
	}
}

func studioVODFilter(ctx *fiber.Ctx) (f repo.StudioVODFilter, errs map[string]string) {
	errs = map[string]string{}

	if v := ctx.Query("visibility"); v != "" {
		if v != entity.VisibilityPublic && v != entity.VisibilityHidden {
			errs["visibility"] = _detailEnum
		}

		f.Visibility = &v
	}

	if s := ctx.Query("status"); s != "" {
		if s != entity.VODReady && s != entity.VODExpired {
			errs["status"] = _detailEnum
		}

		f.Status = &s
	}

	return f, errs
}

// studioVODs godoc
//
//	@Summary  Studio: the channel's VODs (READY and EXPIRED, hidden ones included), newest first
//	@Tags     studio
//	@Produce  json
//	@Security BearerAuth
//	@Param    visibility query    string false "filter" Enums(PUBLIC,HIDDEN)
//	@Param    status     query    string false "filter" Enums(READY,EXPIRED)
//	@Param    cursor     query    string false "nextCursor of the previous page"
//	@Param    limit      query    int    false "1–50" default(24)
//	@Success  200        {object} Data[StudioVODList]
//	@Failure  400        {object} ErrorResponse "VALIDATION_FAILED (visibility / status = ENUM, cursor = INVALID)"
//	@Failure  401        {object} ErrorResponse "UNAUTHORIZED"
//	@Failure  403        {object} ErrorResponse "NOT_CREATOR"
//	@Router   /studio/vods [get]
func (r *routes) studioVODs(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	f, errs := studioVODFilter(ctx)
	if len(errs) > 0 {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid query", errs)
	}

	page, err := r.studio.StudioVODs(ctx.UserContext(), ch.ID, f, ctx.Query("cursor"), ctx.QueryInt("limit", _defaultLim))
	if errors.Is(err, streams.ErrBadCursor) {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid cursor", map[string]string{"cursor": _detailInvalid})
	}

	if err != nil {
		return r.internal(ctx, "studioVODs", err)
	}

	out := StudioVODList{Items: make([]StudioVOD, 0, len(page.Items))}
	for _, v := range page.Items {
		out.Items = append(out.Items, r.studioVODBody(v))
	}

	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(Data[StudioVODList]{Data: out})
}

// VODPatchRequest is the body of PATCH /studio/vods/{id}.
type VODPatchRequest struct {
	Title      *string `json:"title"`
	Visibility *string `json:"visibility" enums:"PUBLIC,HIDDEN"`
}

func (b VODPatchRequest) validate() map[string]string {
	errs := map[string]string{}

	if b.Title == nil && b.Visibility == nil {
		errs["body"] = "REQUIRED"
	}

	if b.Title != nil {
		if n := utf8.RuneCountInString(strings.TrimSpace(*b.Title)); n == 0 || n > _maxTitle {
			errs["title"] = "LENGTH"
		}
	}

	if b.Visibility != nil && *b.Visibility != entity.VisibilityPublic && *b.Visibility != entity.VisibilityHidden {
		errs["visibility"] = _detailEnum
	}

	return errs
}

func vodID(ctx *fiber.Ctx) (id string, ok bool) {
	u, err := uuid.Parse(ctx.Params("id"))
	if err != nil {
		return "", false
	}

	return u.String(), true
}

// updateVOD godoc
//
//	@Summary     Studio: rename, hide or show a VOD
//	@Description A hidden VOD disappears from every public list, search and continue-watching; its URL is 404 for everyone but the owner.
//	@Tags        studio
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string          true "vod id (uuid)"
//	@Param       body body     VODPatchRequest true "fields to change"
//	@Success     200  {object} Data[StudioVOD]
//	@Failure     400  {object} ErrorResponse "VALIDATION_FAILED (body = REQUIRED, title = LENGTH, visibility = ENUM)"
//	@Failure     401  {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403  {object} ErrorResponse "NOT_CREATOR"
//	@Failure     404  {object} ErrorResponse "NOT_FOUND"
//	@Failure     409  {object} ErrorResponse "INVALID_STATE (EXPIRED)"
//	@Router      /studio/vods/{id} [patch]
func (r *routes) updateVOD(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	id, ok := vodID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	}

	var body VODPatchRequest
	if err := ctx.BodyParser(&body); err != nil {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", nil)
	}

	if errs := body.validate(); len(errs) > 0 {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", errs)
	}

	p := entity.VODPatch{Visibility: body.Visibility}
	if body.Title != nil {
		t := strings.TrimSpace(*body.Title)
		p.Title = &t
	}

	v, err := r.studio.UpdateVOD(ctx.UserContext(), ch.ID, id, p)

	switch {
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	case errors.Is(err, entity.ErrInvalidState):
		return errorJSON(ctx, http.StatusConflict, CodeInvalidState, "an expired vod cannot be changed", nil)
	case err != nil:
		return r.internal(ctx, "updateVOD", err)
	}

	return ctx.JSON(Data[StudioVOD]{Data: r.studioVODBody(v)})
}

// deleteVOD godoc
//
//	@Summary     Studio: delete a VOD
//	@Description Soft delete; media-svc then removes its files (vod.deleted.v1). A VOD already deleted is 404.
//	@Tags        studio
//	@Security    BearerAuth
//	@Param       id path string true "vod id (uuid)"
//	@Success     204
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "NOT_CREATOR"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Router      /studio/vods/{id} [delete]
func (r *routes) deleteVOD(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	id, ok := vodID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	}

	err := r.studio.DeleteVOD(ctx.UserContext(), ch.ID, id)

	switch {
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	case err != nil:
		return r.internal(ctx, "deleteVOD", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}
