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
}

type routes struct {
	studio  usecase.Studio
	catalog usecase.Catalog
	l       logger.Interface
	o       Options
}

// NewRoutes registers /v1/studio, /v1/streams and /v1/vods.
func NewRoutes(group fiber.Router, studio usecase.Studio, catalog usecase.Catalog, auth fiber.Handler, l logger.Interface, o Options) {
	r := &routes{studio: studio, catalog: catalog, l: l, o: o}

	st := group.Group("/studio", auth, r.requireCreator)
	{
		st.Post("/stream-key", r.createKey)
		st.Get("/stream-key", r.keyInfo)
		st.Get("/stream", r.overview)
		st.Put("/stream", r.updateSettings)
		st.Post("/stream/end", r.end)
	}

	group.Get("/streams", r.listLive)
	group.Get("/streams/:id", r.getStream)
	group.Get("/vods/:id", r.getVOD)
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
//	@Failure     403 {object} ErrorResponse "NOT_CREATOR"
//	@Router      /studio/stream-key [post]
func (r *routes) createKey(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
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
//	@Failure     403  {object} ErrorResponse "NOT_CREATOR"
//	@Router      /studio/stream [put]
func (r *routes) updateSettings(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	var body SettingsRequest
	if err := ctx.BodyParser(&body); err != nil {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", nil)
	}

	if errs := body.validate(); len(errs) > 0 {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", errs)
	}

	set, err := r.studio.UpdateSettings(ctx.UserContext(), entity.Settings{
		ChannelID: ch.ID,
		Title:     body.Title,
		Tags:      body.Tags,
		Language:  body.Language,
		IsMature:  body.IsMature,
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
//	@Summary     Streams on air (LIVE and INTERRUPTED), newest first
//	@Description channel is null for streams whose channel is unknown to stream-svc.
//	@Tags        streams
//	@Produce     json
//	@Param       status query    string false "only live"              Enums(live) default(live)
//	@Param       cursor query    string false "nextCursor of the previous page"
//	@Param       limit  query    int    false "page size (1–50)"        default(24)
//	@Success     200    {object} Data[StreamList]
//	@Failure     400    {object} ErrorResponse "VALIDATION_FAILED"
//	@Router      /streams [get]
func (r *routes) listLive(ctx *fiber.Ctx) error {
	if s := ctx.Query("status", "live"); s != "live" {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "only status=live is supported", map[string]string{"status": "ONEOF"})
	}

	page, err := r.catalog.ListLive(ctx.UserContext(), ctx.Query("cursor"), ctx.QueryInt("limit", _defaultLim))
	if errors.Is(err, streams.ErrBadCursor) {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid cursor", map[string]string{"cursor": "INVALID"})
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
//	@Summary Stream detail (status, manifestUrl, vodId once the VOD is ready)
//	@Tags    streams
//	@Produce json
//	@Param   id  path     string true "stream id (uuid)"
//	@Success 200 {object} Data[Stream]
//	@Failure 404 {object} ErrorResponse "NOT_FOUND"
//	@Router  /streams/{id} [get]
func (r *routes) getStream(ctx *fiber.Ctx) error {
	id := ctx.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "stream not found", nil)
	}

	s, err := r.catalog.Get(ctx.UserContext(), id)
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
//	@Summary VOD (READY and not expired)
//	@Tags    vods
//	@Produce json
//	@Param   id  path     string true "vod id (uuid)"
//	@Success 200 {object} Data[VOD]
//	@Failure 404 {object} ErrorResponse "NOT_FOUND"
//	@Router  /vods/{id} [get]
func (r *routes) getVOD(ctx *fiber.Ctx) error {
	id := ctx.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	}

	v, err := r.catalog.GetVOD(ctx.UserContext(), id)
	if errors.Is(err, entity.ErrNotFound) {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	}

	if err != nil {
		return r.internal(ctx, "getVOD", err)
	}

	return ctx.JSON(Data[VOD]{Data: VOD{
		ID:           v.ID,
		StreamID:     v.StreamID,
		ChannelID:    v.ChannelID,
		Title:        v.Title,
		DurationSec:  v.DurationSec,
		ManifestURL:  r.objectURL(v.PlaylistPath),
		ThumbnailURL: r.objectURL(v.ThumbnailURL),
		Status:       v.Status,
		CreatedAt:    v.CreatedAt.UTC(),
		ExpiresAt:    v.ExpiresAt.UTC(),
		Channel:      channelRef(v.Channel),
	}})
}

// ---- mapping -------------------------------------------------------------------------------------

// objectURL turns an object key (streams/{id}/master.m3u8) into the URL viewers use.
func (r *routes) objectURL(key string) string {
	if key == "" {
		return ""
	}

	return strings.TrimSuffix(r.o.HLSBase, "/") + "/" + strings.TrimPrefix(key, "/")
}

func (r *routes) streamBody(s entity.Stream) Stream {
	var reason *string

	if s.EndReason != nil {
		v := string(*s.EndReason)
		reason = &v
	}

	return Stream{
		ID:           s.ID,
		ChannelID:    s.ChannelID,
		Title:        s.Title,
		Tags:         s.Tags,
		Language:     s.Language,
		IsMature:     s.IsMature,
		Status:       string(s.Status),
		StartedAt:    utcPtr(s.StartedAt),
		EndedAt:      utcPtr(s.EndedAt),
		EndReason:    reason,
		ManifestURL:  r.objectURL("streams/" + s.ID + "/master.m3u8"),
		ThumbnailURL: r.objectURL("streams/" + s.ID + "/thumb.jpg"),
		VODID:        s.VODID,
		Channel:      channelRef(s.Channel),
	}
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
			ManifestURL:   r.objectURL("streams/" + ov.Current.ID + "/master.m3u8"),
			Health:        ov.Health,
		}
	}

	return out
}

func settingsOf(s entity.Settings) Settings {
	out := Settings{Title: s.Title, Tags: s.Tags, Language: s.Language, IsMature: s.IsMature}
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
