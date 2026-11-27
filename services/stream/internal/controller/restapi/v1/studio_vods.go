package v1

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/streams"
)

// CodeInvalidState is returned when a VOD or an upload cannot change in its current state.
const CodeInvalidState = "INVALID_STATE"

// _maxDescription limits the description of an uploaded video (runes).
const _maxDescription = 5000

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
	out := StudioVOD{
		ID: v.ID, StreamID: v.StreamID, Source: v.Source, Title: v.Title, Description: v.Description,
		DurationSec: v.DurationSec, ThumbnailURL: r.objectURL(v.ThumbnailURL), Status: v.Status, Visibility: v.Visibility,
		IsMature: v.IsMature, Category: categoryRef(v.CategoryID), CreatedAt: v.CreatedAt.UTC(),
		PublishedAt: utcPtr(v.PublishedAt), ExpiresAt: utcPtr(v.ExpiresAt),
	}

	switch v.Status {
	case entity.VODProcessing:
		out.Processing = &VODProcessing{Percent: v.ProcessingPercent, Stage: v.ProcessingStage}
	case entity.VODFailed:
		out.FailureReason, out.Retryable = v.FailureReason, v.FailureRetryable
	default:
		out.ManifestURL = r.manifestURL(v.PlaylistPath, time.Now().Add(entity.PlaybackTTL))
	}

	return out
}

// sourceQuery reads ?source=LIVE|UPLOAD into filter (nil when absent).
func sourceQuery(ctx *fiber.Ctx, errs map[string]string) *string {
	s := ctx.Query("source")
	if s == "" {
		return nil
	}

	if s != entity.SourceLive && s != entity.SourceUpload {
		errs["source"] = _detailEnum
	}

	s = strings.Clone(s)

	return &s
}

func studioVODFilter(ctx *fiber.Ctx) (f repo.StudioVODFilter, errs map[string]string) {
	errs = map[string]string{}

	if v := ctx.Query("visibility"); v != "" {
		if v != entity.VisibilityPublic && v != entity.VisibilityHidden {
			errs["visibility"] = _detailEnum
		}

		v = strings.Clone(v)
		f.Visibility = &v
	}

	if s := ctx.Query("status"); s != "" {
		switch s {
		case entity.VODReady, entity.VODExpired, entity.VODProcessing, entity.VODFailed:
		default:
			errs["status"] = _detailEnum
		}

		s = strings.Clone(s)
		f.Status = &s
	}

	f.Source = sourceQuery(ctx, errs)

	return f, errs
}

// studioVODs godoc
//
//	@Summary     Studio: the channel's VODs (READY, EXPIRED, PROCESSING and FAILED, hidden ones included), newest first
//	@Description Uploaded videos (source UPLOAD) are listed while PROCESSING (processing.percent) and when FAILED (failureReason, retryable); poll every 3 s while one is PROCESSING.
//	@Tags        studio
//	@Produce     json
//	@Security    BearerAuth
//	@Param       visibility query    string false "filter" Enums(PUBLIC,HIDDEN)
//	@Param       status     query    string false "filter" Enums(READY,EXPIRED,PROCESSING,FAILED)
//	@Param       source     query    string false "filter" Enums(LIVE,UPLOAD)
//	@Param       cursor     query    string false "nextCursor of the previous page"
//	@Param       limit      query    int    false "1–50" default(24)
//	@Success     200        {object} Data[StudioVODList]
//	@Failure     400        {object} ErrorResponse "VALIDATION_FAILED (visibility / status / source = ENUM, cursor = INVALID)"
//	@Failure     401        {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403        {object} ErrorResponse "NOT_CREATOR"
//	@Router      /studio/vods [get]
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

// VODPatchRequest is the body of PATCH /studio/vods/{id}. Description, categoryId and isMature apply to uploaded
// videos only.
type VODPatchRequest struct {
	Title       *string `json:"title"`
	Visibility  *string `json:"visibility" enums:"PUBLIC,HIDDEN"`
	Description *string `json:"description"`
	// CategoryID: "" removes the category.
	CategoryID *string `json:"categoryId"`
	IsMature   *bool   `json:"isMature"`
}

func (b VODPatchRequest) validate() map[string]string {
	errs := map[string]string{}

	if b.empty() {
		errs["body"] = "REQUIRED"
	}

	if b.Title != nil {
		if n := utf8.RuneCountInString(strings.TrimSpace(*b.Title)); n == 0 || n > _maxTitle {
			errs["title"] = _detailLength
		}
	}

	if b.Visibility != nil && *b.Visibility != entity.VisibilityPublic && *b.Visibility != entity.VisibilityHidden {
		errs["visibility"] = _detailEnum
	}

	b.validateUploadFields(errs)

	return errs
}

func (b VODPatchRequest) empty() bool {
	return b.Title == nil && b.Visibility == nil && b.Description == nil && b.CategoryID == nil && b.IsMature == nil
}

// validateUploadFields checks the fields only an uploaded video has.
func (b VODPatchRequest) validateUploadFields(errs map[string]string) {
	if b.Description != nil && utf8.RuneCountInString(*b.Description) > _maxDescription {
		errs["description"] = _detailLength
	}

	if b.CategoryID != nil && *b.CategoryID != "" {
		if _, err := uuid.Parse(*b.CategoryID); err != nil {
			errs["categoryId"] = _detailInvalid
		}
	}
}

// patch turns the validated body into the use case's patch (trimmed title, canonical category id).
func (b VODPatchRequest) patch() entity.VODPatch {
	p := entity.VODPatch{Visibility: b.Visibility, Description: b.Description, IsMature: b.IsMature, CategoryID: b.CategoryID}
	if b.Title != nil {
		t := strings.TrimSpace(*b.Title)
		p.Title = &t
	}

	if b.CategoryID != nil && *b.CategoryID != "" {
		p.CategoryID = lowerID(b.CategoryID)
	}

	return p
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
//	@Summary     Studio: rename, hide or show a VOD; describe an uploaded video
//	@Description A hidden VOD disappears from every public list, search and continue-watching; its URL is 404 for everyone but the owner. description, categoryId ("" removes it) and isMature apply to uploaded videos only (409 on a broadcast VOD); an uploaded video can change while READY or PROCESSING, a broadcast VOD while READY.
//	@Tags        studio
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string          true "vod id (uuid)"
//	@Param       body body     VODPatchRequest true "fields to change"
//	@Success     200  {object} Data[StudioVOD]
//	@Failure     400  {object} ErrorResponse "VALIDATION_FAILED (body = REQUIRED, title / description = LENGTH, visibility = ENUM, categoryId = INVALID)"
//	@Failure     401  {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403  {object} ErrorResponse "NOT_CREATOR"
//	@Failure     404  {object} ErrorResponse "NOT_FOUND"
//	@Failure     409  {object} ErrorResponse "INVALID_STATE (EXPIRED, FAILED, or an upload-only field on a broadcast VOD)"
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

	v, err := r.studio.UpdateVOD(ctx.UserContext(), ch.ID, id, body.patch())

	switch {
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	case errors.Is(err, entity.ErrInvalidState):
		return errorJSON(ctx, http.StatusConflict, CodeInvalidState, "the vod cannot be changed in its current state", nil)
	case err != nil:
		return r.internal(ctx, "updateVOD", err)
	}

	return ctx.JSON(Data[StudioVOD]{Data: r.studioVODBody(v)})
}

// deleteVOD godoc
//
//	@Summary     Studio: delete a VOD
//	@Description Soft delete of a READY, EXPIRED, PROCESSING or FAILED VOD; media-svc then removes its files (vod.deleted.v1 carries playlistPrefix). A VOD already deleted is 404.
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

// retryVOD godoc
//
//	@Summary     Studio: transcode a FAILED uploaded video again
//	@Description Only for an upload FAILED with retryable = true while its source file is still stored (3 days); the VOD goes back to PROCESSING.
//	@Tags        studio
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "vod id (uuid)"
//	@Success     202 {object} Data[StudioVOD]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "NOT_CREATOR, CHANNEL_SUSPENDED"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Failure     409 {object} ErrorResponse "INVALID_STATE (not FAILED, not retryable, broadcast VOD or source gone)"
//	@Failure     503 {object} ErrorResponse "STORAGE_UNAVAILABLE"
//	@Router      /studio/vods/{id}/retry [post]
func (r *routes) retryVOD(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	id, ok := vodID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	}

	v, err := r.uploads.Retry(ctx.UserContext(), ch, id)
	if err != nil {
		return r.uploadError(ctx, "retryVOD", err)
	}

	return ctx.Status(http.StatusAccepted).JSON(Data[StudioVOD]{Data: r.studioVODBody(v)})
}
