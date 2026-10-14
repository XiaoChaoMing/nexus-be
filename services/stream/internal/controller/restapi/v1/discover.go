package v1

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase/streams"
)

const (
	_maxCategories = 30
	_maxTagFilters = 5

	_detailEnum    = "ENUM"
	_detailInvalid = "INVALID"
)

var _languageRe = regexp.MustCompile(`^[a-z]{2}$`)

// csv splits a comma-separated query value, dropping empty parts.
func csv(s string) []string {
	out := []string{}

	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// categoryIDs parses 1–30 category uuids; ok is false when one is malformed or there are too many.
func categoryIDs(s string) (ids []string, ok bool) {
	ids = csv(s)
	if len(ids) > _maxCategories {
		return nil, false
	}

	for i, id := range ids {
		u, err := uuid.Parse(id)
		if err != nil {
			return nil, false
		}

		ids[i] = u.String()
	}

	return ids, true
}

func streamFilter(ctx *fiber.Ctx) (f usecase.StreamFilter, errs map[string]string) {
	errs = map[string]string{}
	f = usecase.StreamFilter{Sort: usecase.StreamSort(ctx.Query("sort", string(usecase.SortViewers)))}

	if !f.Sort.Valid() {
		errs["sort"] = _detailEnum
	}

	ids, ok := categoryIDs(ctx.Query("category"))
	if !ok {
		errs["category"] = _detailInvalid
	}

	f.CategoryIDs = ids

	if lang := ctx.Query("language"); lang != "" {
		if !_languageRe.MatchString(lang) {
			errs["language"] = "FORMAT"
		}

		f.Language = strings.Clone(lang)
	}

	if f.Tags = csv(ctx.Query("tags")); len(f.Tags) > _maxTagFilters {
		errs["tags"] = "TAG_COUNT"
	}

	return f, errs
}

func (r *routes) streamList(items []entity.Stream) StreamList {
	out := StreamList{Items: make([]Stream, 0, len(items))}
	for _, s := range items {
		out.Items = append(out.Items, r.streamBody(s))
	}

	return out
}

// recommended godoc
//
//	@Summary     Recommended live streams (Hero, "Live đề xuất", Guest sidebar)
//	@Description LIVE only (not INTERRUPTED), never 18+, by viewers then the most recent start. Not paged (nextCursor is null).
//	@Tags        streams
//	@Produce     json
//	@Param       limit query    int false "1–24" default(12)
//	@Success     200   {object} Data[StreamList]
//	@Router      /streams/recommended [get]
func (r *routes) recommended(ctx *fiber.Ctx) error {
	items, err := r.catalog.Recommended(ctx.UserContext(), ctx.QueryInt("limit", streams.DefaultRecommended))
	if err != nil {
		return r.internal(ctx, "recommended", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(Data[StreamList]{Data: r.streamList(items)})
}

func (r *routes) vodList(page usecase.VODPage) VODList {
	out := VODList{Items: make([]VOD, 0, len(page.Items))}
	for _, v := range page.Items {
		out.Items = append(out.Items, r.vodBody(v))
	}

	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}

	return out
}

func (r *routes) sendVODPage(ctx *fiber.Ctx, op string, page usecase.VODPage, err error) error {
	switch {
	case errors.Is(err, streams.ErrBadCursor):
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid cursor", map[string]string{"cursor": _detailInvalid})
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "channel not found", nil)
	case err != nil:
		return r.internal(ctx, op, err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(Data[VODList]{Data: r.vodList(page)})
}

// listVODs godoc
//
//	@Summary Public VODs (READY, PUBLIC, not expired), newest first
//	@Tags    vods
//	@Produce json
//	@Param   category query    string false "category ids, comma-separated"
//	@Param   cursor   query    string false "nextCursor of the previous page"
//	@Param   limit    query    int    false "1–50" default(24)
//	@Success 200      {object} Data[VODList]
//	@Failure 400      {object} ErrorResponse "VALIDATION_FAILED (category = INVALID, cursor = INVALID)"
//	@Router  /vods [get]
func (r *routes) listVODs(ctx *fiber.Ctx) error {
	ids, ok := categoryIDs(ctx.Query("category"))
	if !ok {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid query", map[string]string{"category": _detailInvalid})
	}

	page, err := r.catalog.VODs(ctx.UserContext(), repo.VODFilter{CategoryIDs: ids}, ctx.Query("cursor"), ctx.QueryInt("limit", _defaultLim))

	return r.sendVODPage(ctx, "listVODs", page, err)
}

// recommendedVODs godoc
//
//	@Summary Recommended VODs (newest public ones, never 18+); not paged
//	@Tags    vods
//	@Produce json
//	@Param   limit query    int false "1–24" default(12)
//	@Success 200   {object} Data[VODList]
//	@Router  /vods/recommended [get]
func (r *routes) recommendedVODs(ctx *fiber.Ctx) error {
	items, err := r.catalog.RecommendedVODs(ctx.UserContext(), ctx.QueryInt("limit", streams.DefaultRecommended))

	return r.sendVODPage(ctx, "recommendedVODs", usecase.VODPage{Items: items}, err)
}

// channelVODs godoc
//
//	@Summary A channel's public VODs (Video tab), newest first
//	@Tags    vods
//	@Produce json
//	@Param   id     path     string true  "channel id (uuid)"
//	@Param   cursor query    string false "nextCursor of the previous page"
//	@Param   limit  query    int    false "1–50" default(24)
//	@Success 200    {object} Data[VODList]
//	@Failure 400    {object} ErrorResponse "VALIDATION_FAILED (cursor = INVALID)"
//	@Failure 404    {object} ErrorResponse "NOT_FOUND"
//	@Router  /channels/{id}/vods [get]
func (r *routes) channelVODs(ctx *fiber.Ctx) error {
	u, err := uuid.Parse(ctx.Params("id"))
	if err != nil {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "channel not found", nil)
	}

	page, err := r.catalog.ChannelVODs(ctx.UserContext(), u.String(), ctx.Query("cursor"), ctx.QueryInt("limit", _defaultLim))

	return r.sendVODPage(ctx, "channelVODs", page, err)
}
