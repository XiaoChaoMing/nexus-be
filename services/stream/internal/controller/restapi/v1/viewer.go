package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

// HeaderAnonID carries the guest's browser id (a UUID the web app keeps in localStorage).
const HeaderAnonID = "X-NXL-Anon-Id"

// PlaybackRequest is the optional body of the playback endpoints.
type PlaybackRequest struct {
	// AcknowledgeMature confirms the viewer is 18+; signed-in viewers are not asked again for the channel.
	AcknowledgeMature bool `json:"acknowledgeMature"`
}

// Mature -.
type Mature struct {
	Required     bool `json:"required"`
	Acknowledged bool `json:"acknowledged"`
}

// Playback is returned by the playback endpoints. While the age gate is closed only `mature` (and `anonId`) is set.
type Playback struct {
	SessionID   *string    `json:"sessionId"`
	ManifestURL *string    `json:"manifestUrl"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	// AnonID is echoed (or generated) for guests; the client stores it and sends it back in X-NXL-Anon-Id.
	AnonID *string `json:"anonId,omitempty"`
	Mature Mature  `json:"mature"`
}

// ProgressRequest is the body of PUT /me/watch-progress/{vodId}.
type ProgressRequest struct {
	PositionSec *int `json:"positionSec"`
	// KeepFurther keeps the stored position when it is further (merging a browser's local progress on sign-in).
	KeepFurther bool `json:"keepFurther"`
}

// ContinueItem -.
type ContinueItem struct {
	VOD         VOD       `json:"vod"`
	PositionSec int       `json:"positionSec"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ContinueList -.
type ContinueList struct {
	Items []ContinueItem `json:"items"`
}

// VODViewer is the signed-in caller's state for a VOD.
type VODViewer struct {
	PositionSec *int `json:"positionSec"`
}

// openStream godoc
//
//	@Summary     Open a live (or ended) stream for playback
//	@Description Guests may call it; send X-NXL-Anon-Id when you have one. Mature streams return mature.required=true and no manifestUrl until called again with acknowledgeMature=true (signed-in viewers are remembered per channel). manifestUrl gains a signature in GĐ5.
//	@Tags        playback
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id             path     string          true  "stream id (uuid)"
//	@Param       X-NXL-Anon-Id  header   string          false "guest browser id (uuid)"
//	@Param       body           body     PlaybackRequest false "age confirmation"
//	@Success     200            {object} Data[Playback]
//	@Failure     400            {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401            {object} ErrorResponse "UNAUTHORIZED (token sent but invalid)"
//	@Failure     404            {object} ErrorResponse "NOT_FOUND"
//	@Router      /streams/{id}/playback [post]
func (r *routes) openStream(ctx *fiber.Ctx) error {
	return r.open(ctx, "stream")
}

// openVOD godoc
//
//	@Summary     Open a VOD for playback
//	@Description Same rules as /streams/{id}/playback; a VOD inherits the mature flag of its broadcast.
//	@Tags        playback
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id             path     string          true  "vod id (uuid)"
//	@Param       X-NXL-Anon-Id  header   string          false "guest browser id (uuid)"
//	@Param       body           body     PlaybackRequest false "age confirmation"
//	@Success     200            {object} Data[Playback]
//	@Failure     400            {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401            {object} ErrorResponse "UNAUTHORIZED (token sent but invalid)"
//	@Failure     404            {object} ErrorResponse "NOT_FOUND"
//	@Router      /vods/{id}/playback [post]
func (r *routes) openVOD(ctx *fiber.Ctx) error {
	return r.open(ctx, "vod")
}

func (r *routes) open(ctx *fiber.Ctx, kind string) error {
	id := ctx.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, kind+" not found", nil)
	}

	var body PlaybackRequest
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(&body); err != nil {
			return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", nil)
		}
	}

	anonID := ctx.Get(HeaderAnonID)
	if anonID != "" {
		if _, err := uuid.Parse(anonID); err != nil {
			return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid anonymous id",
				map[string]string{"anonId": _detailInvalid})
		}
	}

	userID, _ := middleware.UserID(ctx)

	in := usecase.PlaybackInput{UserID: userID, AnonID: anonID, AcknowledgeMature: body.AcknowledgeMature}
	if kind == "vod" {
		in.Target.VODID = id
	} else {
		in.Target.StreamID = id
	}

	g, err := r.viewer.Open(ctx.UserContext(), in)
	if errors.Is(err, entity.ErrNotFound) {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, kind+" not found", nil)
	}

	if err != nil {
		return r.internal(ctx, "open "+kind, err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(Data[Playback]{Data: r.playbackBody(g)})
}

func (r *routes) playbackBody(g entity.PlaybackGrant) Playback {
	out := Playback{Mature: Mature{Required: g.Mature.Required, Acknowledged: g.Mature.Acknowledged}}

	if g.AnonID != "" {
		out.AnonID = &g.AnonID
	}

	if g.Session != nil {
		id, url, exp := g.Session.ID, r.manifestURL(g.ObjectKey), g.Session.ExpiresAt.UTC()
		out.SessionID, out.ManifestURL, out.ExpiresAt = &id, &url, &exp
	}

	return out
}

// manifestURL is the single place playback URLs are built, so GĐ5 can add the secure_link signature here.
func (r *routes) manifestURL(key string) string {
	return r.objectURL(key)
}

// saveProgress godoc
//
//	@Summary     Save the VOD position of the signed-in viewer
//	@Description Clients send it at most every 15 s and when leaving. keepFurther=true keeps a further stored position (used to merge a browser's local progress after sign-in).
//	@Tags        playback
//	@Accept      json
//	@Security    BearerAuth
//	@Param       vodId path string          true "vod id (uuid)"
//	@Param       body  body ProgressRequest true "position"
//	@Success     204
//	@Failure     400 {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Router      /me/watch-progress/{vodId} [put]
func (r *routes) saveProgress(ctx *fiber.Ctx) error {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	vodID := ctx.Params("vodId")
	if _, err := uuid.Parse(vodID); err != nil {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	}

	var body ProgressRequest
	if err := ctx.BodyParser(&body); err != nil {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", nil)
	}

	if body.PositionSec == nil {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body",
			map[string]string{"positionSec": "REQUIRED"})
	}

	err := r.viewer.SaveProgress(ctx.UserContext(),
		entity.WatchProgress{UserID: userID, VODID: vodID, PositionSec: *body.PositionSec}, body.KeepFurther)

	switch {
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "vod not found", nil)
	case errors.Is(err, entity.ErrInvalidPosition):
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "position out of range",
			map[string]string{"positionSec": "RANGE"})
	case err != nil:
		return r.internal(ctx, "saveProgress", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// continueWatching godoc
//
//	@Summary     VODs the signed-in viewer has not finished, newest first
//	@Description Positions below 5 s and VODs watched to within 10 s of the end are left out, like expired VODs.
//	@Tags        playback
//	@Produce     json
//	@Security    BearerAuth
//	@Param       limit query    int false "1–50" default(12)
//	@Success     200   {object} Data[ContinueList]
//	@Failure     401   {object} ErrorResponse "UNAUTHORIZED"
//	@Router      /me/continue-watching [get]
func (r *routes) continueWatching(ctx *fiber.Ctx) error {
	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	items, err := r.viewer.ContinueWatching(ctx.UserContext(), userID, ctx.QueryInt("limit", 0))
	if err != nil {
		return r.internal(ctx, "continueWatching", err)
	}

	out := ContinueList{Items: make([]ContinueItem, 0, len(items))}
	for _, it := range items {
		out.Items = append(out.Items, ContinueItem{VOD: r.vodBody(it.VOD), PositionSec: it.PositionSec, UpdatedAt: it.UpdatedAt.UTC()})
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(Data[ContinueList]{Data: out})
}
