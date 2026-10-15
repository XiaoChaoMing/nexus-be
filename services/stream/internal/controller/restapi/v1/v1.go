// Package v1 is the public REST API of stream-svc.
package v1

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/streams"
)

// Error codes shared with the web client.
const (
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeNotCreator       = "NOT_CREATOR"
	CodeNotFound         = "NOT_FOUND"
	CodeNotLive          = "NOT_LIVE"
	CodeChannelSuspended = "CHANNEL_SUSPENDED"
	CodeInternal         = "INTERNAL_ERROR"

	_maxTitle   = 140
	_maxTags    = 10
	_maxTagLen  = 25
	_maxLang    = 10
	_defaultLim = 24

	_localChannel = "channel"
)

// Options -.
type Options struct {
	RTMPPublicURL string
	HLSBase       string
	// HLSSigning signs playback URLs with HLSSecret (nginx secure_link) and empties the public manifestUrl.
	HLSSigning bool
	HLSSecret  string
}

type routes struct {
	studio  usecase.Studio
	catalog usecase.Catalog
	viewer  usecase.Viewer
	l       logger.Interface
	o       Options
}

// Auth holds the two authentication middlewares: Required rejects guests, Optional identifies callers when a
// token is sent.
type Auth struct {
	Required fiber.Handler
	Optional fiber.Handler
}

// NewRoutes registers /v1/studio, /v1/streams, /v1/vods and the viewer's /v1/me/watch-progress and
// /v1/me/continue-watching.
func NewRoutes(group fiber.Router, studio usecase.Studio, catalog usecase.Catalog, viewer usecase.Viewer, auth Auth,
	l logger.Interface, o Options,
) {
	r := &routes{studio: studio, catalog: catalog, viewer: viewer, l: l, o: o}

	st := group.Group("/studio", auth.Required, r.requireCreator)
	{
		st.Post("/stream-key", r.createKey)
		st.Get("/stream-key", r.keyInfo)
		st.Get("/stream", r.overview)
		st.Put("/stream", r.updateSettings)
		st.Post("/stream/end", r.end)
		st.Get("/dashboard", r.dashboard)
		st.Get("/vods", r.studioVODs)
		st.Patch("/vods/:id", r.updateVOD)
		st.Delete("/vods/:id", r.deleteVOD)
	}

	// Static routes before the :id ones of the same prefix.
	group.Get("/streams/recommended", r.recommended)
	group.Get("/streams", r.listLive)
	group.Get("/streams/:id", auth.Optional, r.getStream)
	group.Post("/streams/:id/playback", auth.Optional, r.openStream)
	group.Get("/vods/recommended", r.recommendedVODs)
	group.Get("/vods", r.listVODs)
	group.Get("/vods/:id", auth.Optional, r.getVOD)
	group.Post("/vods/:id/playback", auth.Optional, r.openVOD)
	group.Get("/channels/:id/vods", r.channelVODs)

	group.Put("/me/watch-progress/:vodId", auth.Required, r.saveProgress)
	group.Get("/me/continue-watching", auth.Required, r.continueWatching)
}

func errorJSON(ctx *fiber.Ctx, status int, code, message string, details map[string]string) error {
	return ctx.Status(status).JSON(ErrorResponse{Error: ErrorBody{Code: code, Message: message, Details: details}})
}

func (r *routes) internal(ctx *fiber.Ctx, op string, err error) error {
	r.l.Error("restapi - v1 - %s: %v", op, err)

	return errorJSON(ctx, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
}

// requireCreator resolves the caller's channel once per Studio request; callers without one get
// 403 NOT_CREATOR on every /studio route.
func (r *routes) requireCreator(ctx *fiber.Ctx) error {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	ch, err := r.studio.Channel(ctx.UserContext(), userID)

	switch {
	case errors.Is(err, entity.ErrNotCreator):
		return notCreator(ctx)
	case err != nil:
		return r.internal(ctx, "requireCreator", err)
	}

	ctx.Locals(_localChannel, ch)

	return ctx.Next()
}

func notCreator(ctx *fiber.Ctx) error {
	return errorJSON(ctx, http.StatusForbidden, CodeNotCreator, "only creators can use the Studio", nil)
}

func channelSuspended(ctx *fiber.Ctx) error {
	return errorJSON(ctx, http.StatusForbidden, CodeChannelSuspended, "channel suspended", nil)
}

// creatorChannel returns the channel stored by requireCreator.
func creatorChannel(ctx *fiber.Ctx) (entity.Channel, bool) {
	ch, ok := ctx.Locals(_localChannel).(entity.Channel)

	return ch, ok && ch.ID != ""
}

// ---- studio --------------------------------------------------------------------------------------

// createKey godoc
//
//	@Summary     Create / regenerate the stream key
//	@Description The key is returned only here, once. The previous key stops working immediately.
//	@Tags        studio
//	@Produce     json
//	@Security    BearerAuth
//	@Success     201 {object} Data[StreamKeyCreated]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "NOT_CREATOR, CHANNEL_SUSPENDED"
//	@Router      /studio/stream-key [post]
func (r *routes) createKey(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	if ch.Suspended {
		return channelSuspended(ctx)
	}

	key, err := r.studio.CreateKey(ctx.UserContext(), ch.ID)
	if err != nil {
		return r.internal(ctx, "createKey", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.Status(http.StatusCreated).JSON(Data[StreamKeyCreated]{Data: StreamKeyCreated{StreamKey: key, RTMPURL: r.o.RTMPPublicURL}})
}

// keyInfo godoc
//
//	@Summary  Stream key status (never the key itself)
//	@Tags     studio
//	@Produce  json
//	@Security BearerAuth
//	@Success  200 {object} Data[StreamKeyInfo]
//	@Failure  401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure  403 {object} ErrorResponse "NOT_CREATOR"
//	@Router   /studio/stream-key [get]
func (r *routes) keyInfo(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	info, err := r.studio.KeyInfo(ctx.UserContext(), ch.ID)
	if err != nil {
		return r.internal(ctx, "keyInfo", err)
	}

	return ctx.JSON(Data[StreamKeyInfo]{Data: StreamKeyInfo{HasKey: info.HasKey, RTMPURL: r.o.RTMPPublicURL, CreatedAt: utcPtr(info.CreatedAt)}})
}

// overview godoc
//
//	@Summary     Studio: the creator's channel, settings and the current broadcast
//	@Description current is null when the channel is offline; health is the latest ingest report (5 s).
//	@Tags        studio
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} Data[Studio]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "NOT_CREATOR"
//	@Router      /studio/stream [get]
func (r *routes) overview(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	ov, err := r.studio.Overview(ctx.UserContext(), ch.ID)
	if err != nil {
		return r.internal(ctx, "overview", err)
	}

	return ctx.JSON(Data[Studio]{Data: r.studioBody(ch, ov)})
}

// SettingsRequest is the body of PUT /studio/stream.
type SettingsRequest struct {
	Title    string   `json:"title"`
	Tags     []string `json:"tags"`
	Language string   `json:"language"`
	IsMature bool     `json:"isMature"`
	// CategoryID: absent or null removes the category (PUT replaces the settings).
	CategoryID *string `json:"categoryId"`
}

func (b SettingsRequest) validate() map[string]string {
	errs := map[string]string{}

	if n := utf8.RuneCountInString(strings.TrimSpace(b.Title)); n == 0 || n > _maxTitle {
		errs["title"] = "LENGTH"
	}

	if len(b.Tags) > _maxTags {
		errs["tags"] = "MAX"
	}

	for _, t := range b.Tags {
		if utf8.RuneCountInString(strings.TrimSpace(t)) > _maxTagLen {
			errs["tags"] = "TAG_LENGTH"
		}
	}

	if len(b.Language) > _maxLang {
		errs["language"] = "LENGTH"
	}

	if b.CategoryID != nil {
		if _, err := uuid.Parse(*b.CategoryID); err != nil {
			errs["categoryId"] = _detailInvalid
		}
	}

	return errs
}

// updateSettings godoc
//
//	@Summary     Update title / tags / mature flag
//	@Description Applies to the next broadcast and to the one on air, if any. Title 1–140, at most 10 tags of ≤ 25 characters.
//	@Tags        studio
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     SettingsRequest true "Settings"
//	@Success     200  {object} Data[Settings]
//	@Failure     400  {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401  {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403  {object} ErrorResponse "NOT_CREATOR, CHANNEL_SUSPENDED"
//	@Router      /studio/stream [put]
func (r *routes) updateSettings(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	if ch.Suspended {
		return channelSuspended(ctx)
	}

	var body SettingsRequest
	if err := ctx.BodyParser(&body); err != nil {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", nil)
	}

	if errs := body.validate(); len(errs) > 0 {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", errs)
	}

	set, err := r.studio.UpdateSettings(ctx.UserContext(), entity.Settings{
		ChannelID:  ch.ID,
		Title:      body.Title,
		Tags:       body.Tags,
		Language:   body.Language,
		IsMature:   body.IsMature,
		CategoryID: lowerID(body.CategoryID),
	})
	if err != nil {
		return r.internal(ctx, "updateSettings", err)
	}

	return ctx.JSON(Data[Settings]{Data: settingsOf(set)})
}

// end godoc
//
//	@Summary     End the broadcast
//	@Description Moves the stream to ENDED; media-svc disconnects OBS and publishes the VOD.
//	@Tags        studio
//	@Security    BearerAuth
//	@Success     204
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "NOT_CREATOR"
//	@Failure     409 {object} ErrorResponse "NOT_LIVE"
//	@Router      /studio/stream/end [post]
func (r *routes) end(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	err := r.studio.End(ctx.UserContext(), ch.ID)

	switch {
	case errors.Is(err, entity.ErrNotLive):
		return errorJSON(ctx, http.StatusConflict, CodeNotLive, "channel is not live", nil)
	case err != nil:
		return r.internal(ctx, "end", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// ---- catalog -------------------------------------------------------------------------------------

// listLive godoc
//
//	@Summary     Streams on air (LIVE and INTERRUPTED), mature ones included (isMature)
//	@Description sort=viewers (default; trending is the same) orders by live viewers, then the most recent start; sort=newest by start. category is 1–30 category ids (comma-separated, from GET /v1/categories); tags must all match. channel is null for streams whose channel is unknown to stream-svc.
//	@Tags        streams
//	@Produce     json
//	@Param       status   query    string false "only live"              Enums(live) default(live)
//	@Param       category query    string false "category ids, comma-separated"
//	@Param       language query    string false "two-letter language code"
//	@Param       tags     query    string false "up to 5 tags, comma-separated"
//	@Param       sort     query    string false "order" Enums(viewers,newest,trending) default(viewers)
//	@Param       cursor   query    string false "nextCursor of the previous page"
//	@Param       limit    query    int    false "page size (1–50)"        default(24)
//	@Success     200      {object} Data[StreamList]
//	@Failure     400      {object} ErrorResponse "VALIDATION_FAILED (category = INVALID, language = FORMAT, tags = TAG_COUNT, sort = ENUM, cursor = INVALID)"
//	@Router      /streams [get]
func (r *routes) listLive(ctx *fiber.Ctx) error {
	if s := ctx.Query("status", "live"); s != "live" {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "only status=live is supported", map[string]string{"status": "ONEOF"})
	}

	f, errs := streamFilter(ctx)
	if len(errs) > 0 {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid query", errs)
	}

	page, err := r.catalog.List(ctx.UserContext(), f, ctx.Query("cursor"), ctx.QueryInt("limit", _defaultLim))
	if errors.Is(err, streams.ErrBadCursor) {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid cursor", map[string]string{"cursor": _detailInvalid})
	}

	if err != nil {
		return r.internal(ctx, "listLive", err)
	}

	out := StreamList{Items: make([]Stream, 0, len(page.Items))}
	for _, s := range page.Items {
		out.Items = append(out.Items, r.streamBody(s))
	}

	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(Data[StreamList]{Data: out})
}

// getStream godoc
//
//	@Summary     Stream detail (status, manifestUrl, vodId once the VOD is ready)
//	@Description status may be SCHEDULED (an announced occurrence: scheduleId, scheduledStartAt) or CANCELLED. vodId is null while the VOD is hidden, except for the channel owner.
//	@Tags        streams
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "stream id (uuid)"
//	@Success     200 {object} Data[Stream]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED (token sent but invalid)"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Router      /streams/{id} [get]
func (r *routes) getStream(ctx *fiber.Ctx) error {
	id := ctx.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "stream not found", nil)
	}

	userID, _ := middleware.UserID(ctx)

	s, err := r.catalog.GetFor(ctx.UserContext(), id, userID)
	if errors.Is(err, entity.ErrNotFound) {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "stream not found", nil)
	}

	if err != nil {
		return r.internal(ctx, "getStream", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(Data[Stream]{Data: r.streamBody(s)})
}

// getVOD godoc
//
//	@Summary     VOD (READY and not expired)
//	@Description With a token, viewer.positionSec is the caller's saved position (null when never watched). A HIDDEN VOD is 404 except for its channel owner.
//	@Tags        vods
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "vod id (uuid)"
//	@Success     200 {object} Data[VOD]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED (token sent but invalid)"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Router      /vods/{id} [get]
func (r *routes) getVOD(ctx *fiber.Ctx) error {
	id := ctx.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	}

	userID, _ := middleware.UserID(ctx)

	v, err := r.catalog.GetVODFor(ctx.UserContext(), id, userID)
	if errors.Is(err, entity.ErrNotFound) {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	}

	if err != nil {
		return r.internal(ctx, "getVOD", err)
	}

	body := r.vodBody(v)

	if userID != "" {
		pos, perr := r.viewer.Progress(ctx.UserContext(), userID, v.ID)
		if perr != nil {
			return r.internal(ctx, "getVOD - progress", perr)
		}

		body.Viewer = &VODViewer{PositionSec: pos}

		ctx.Set(fiber.HeaderCacheControl, "no-store")
	}

	return ctx.JSON(Data[VOD]{Data: body})
}

func (r *routes) vodBody(v entity.VOD) VOD {
	return VOD{
		ID:           v.ID,
		StreamID:     v.StreamID,
		ChannelID:    v.ChannelID,
		Title:        v.Title,
		DurationSec:  v.DurationSec,
		ManifestURL:  r.publicManifest(v.PlaylistPath),
		ThumbnailURL: r.objectURL(v.ThumbnailURL),
		Status:       v.Status,
		Visibility:   v.Visibility,
		IsMature:     v.IsMature,
		Category:     categoryRef(v.CategoryID),
		CreatedAt:    v.CreatedAt.UTC(),
		ExpiresAt:    v.ExpiresAt.UTC(),
		Channel:      channelRef(v.Channel),
	}
}

// ---- mapping -------------------------------------------------------------------------------------

// objectURL turns an object key (streams/{id}/master.m3u8) into the URL viewers use.
func (r *routes) objectURL(key string) string {
	if key == "" {
		return ""
	}

	return strings.TrimSuffix(r.o.HLSBase, "/") + "/" + strings.TrimPrefix(key, "/")
}

// publicManifest is the manifestUrl of the public DTOs: empty once URLs are signed (deprecated field; viewers
// get a signed URL from the playback endpoints).
func (r *routes) publicManifest(key string) string {
	if r.o.HLSSigning {
		return ""
	}

	return r.objectURL(key)
}

func (r *routes) streamBody(s entity.Stream) Stream {
	var reason *string

	if s.EndReason != nil {
		v := string(*s.EndReason)
		reason = &v
	}

	return Stream{
		ID:               s.ID,
		ChannelID:        s.ChannelID,
		Title:            s.Title,
		Tags:             s.Tags,
		Language:         s.Language,
		IsMature:         s.IsMature,
		Status:           string(s.Status),
		StartedAt:        utcPtr(s.StartedAt),
		EndedAt:          utcPtr(s.EndedAt),
		EndReason:        reason,
		ManifestURL:      r.publicManifest("streams/" + s.ID + "/master.m3u8"),
		ThumbnailURL:     r.objectURL("streams/" + s.ID + "/thumb.jpg"),
		VODID:            s.VODID,
		ViewerCount:      s.ViewerCount,
		Channel:          channelRef(s.Channel),
		Category:         categoryRef(s.CategoryID),
		ScheduleID:       s.ScheduleID,
		ScheduledStartAt: utcPtr(s.ScheduledStartAt),
	}
}

func categoryRef(id *string) *CategoryRef {
	if id == nil {
		return nil
	}

	return &CategoryRef{ID: *id}
}

// lowerID returns the canonical (lower-case) form of a validated uuid.
func lowerID(id *string) *string {
	if id == nil {
		return nil
	}

	u, err := uuid.Parse(*id)
	if err != nil {
		return nil
	}

	s := u.String()

	return &s
}

func channelRef(c *entity.Channel) *Channel {
	if c == nil {
		return nil
	}

	out := channelOf(*c)

	return &out
}

func channelOf(c entity.Channel) Channel {
	return Channel{ID: c.ID, Handle: c.Handle, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL}
}

func (r *routes) studioBody(ch entity.Channel, ov usecase.StudioOverview) Studio {
	out := Studio{Channel: channelOf(ch), Settings: settingsOf(ov.Settings)}

	if ov.Current != nil {
		out.Current = &StudioStream{
			StreamID:      ov.Current.ID,
			Status:        string(ov.Current.Status),
			StartedAt:     utcPtr(ov.Current.StartedAt),
			InterruptedAt: utcPtr(ov.Current.InterruptedAt),
			ManifestURL:   r.manifestURL("streams/"+ov.Current.ID+"/master.m3u8", time.Now().Add(entity.PlaybackTTL)),
			Health:        ov.Health,
		}
	}

	return out
}

func settingsOf(s entity.Settings) Settings {
	out := Settings{Title: s.Title, Tags: s.Tags, Language: s.Language, IsMature: s.IsMature, CategoryID: s.CategoryID}
	if !s.UpdatedAt.IsZero() {
		t := s.UpdatedAt.UTC()
		out.UpdatedAt = &t
	}

	if out.Tags == nil {
		out.Tags = []string{}
	}

	return out
}

// utcPtr keeps every timestamp of the API in UTC whatever the server's time zone.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	u := t.UTC()

	return &u
}
