package v1

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/channel/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/search"
)

// categories godoc
//
//	@Summary     Categories (Browse)
//	@Description Not paged (nextCursor is always null). sort=viewers orders by live viewers, ties keep the seed order.
//	@Tags        categories
//	@Produce     json
//	@Param       group query    string false "group" Enums(GAMES,ESPORTS,MUSIC,IRL,CREATIVE)
//	@Param       sort  query    string false "order" Enums(position,viewers) default(position)
//	@Success     200   {object} response.Data[response.CategoryList]
//	@Failure     400   {object} response.Error "VALIDATION_FAILED (group / sort = ENUM)"
//	@Router      /categories [get]
func (r *routes) categories(ctx *fiber.Ctx) error {
	errs := map[string]string{}

	var group *entity.CategoryGroup

	if g := ctx.Query("group"); g != "" {
		cg := entity.CategoryGroup(strings.ToUpper(g))
		if !cg.Valid() {
			errs["group"] = entity.DetailEnum
		}

		group = &cg
	}

	sort := entity.CategorySort(ctx.Query("sort", string(entity.CategoryByPosition)))
	if !sort.Valid() {
		errs["sort"] = entity.DetailEnum
	}

	if len(errs) > 0 {
		return invalid(ctx, errs)
	}

	list, err := r.uc.Catalog.Categories(ctx.UserContext(), group, sort)
	if err != nil {
		return r.fail(ctx, "categories", err)
	}

	out := response.CategoryList{Items: make([]response.Category, 0, len(list))}
	for _, c := range list {
		out.Items = append(out.Items, response.CategoryOf(c))
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(response.Data[response.CategoryList]{Data: out})
}

// category godoc
//
//	@Summary Category by slug
//	@Tags    categories
//	@Produce json
//	@Param   slug path     string true "category slug"
//	@Success 200  {object} response.Data[response.Category]
//	@Failure 404  {object} response.Error "NOT_FOUND"
//	@Router  /categories/{slug} [get]
func (r *routes) category(ctx *fiber.Ctx) error {
	c, err := r.uc.Catalog.Category(ctx.UserContext(), strings.ToLower(ctx.Params("slug")))
	if err != nil {
		return r.fail(ctx, "category", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-cache")

	return ctx.JSON(response.Data[response.Category]{Data: response.CategoryOf(c)})
}

// suggest godoc
//
//	@Summary     Search suggestions grouped by kind (Ctrl+K)
//	@Description Accents and case are ignored ("lien minh" finds "Liên Minh"); words match by prefix. Groups are channel, stream, category, vod in that order and only when they have hits.
//	@Tags        search
//	@Produce     json
//	@Param       q     query    string true  "2–100 characters after trimming"
//	@Param       types query    string false "comma-separated subset of channel,stream,category,vod"
//	@Param       limit query    int    false "per group, 1–10" default(5)
//	@Success     200   {object} response.Data[response.Suggestions]
//	@Failure     400   {object} response.Error "VALIDATION_FAILED (q = LENGTH, types = ENUM)"
//	@Router      /search/suggest [get]
func (r *routes) suggest(ctx *fiber.Ctx) error {
	types := []entity.DocType{}

	if t := ctx.Query("types"); t != "" {
		for _, s := range strings.Split(t, ",") {
			dt := entity.DocType(strings.TrimSpace(strings.ToLower(s)))
			if !dt.Valid() {
				return invalid(ctx, map[string]string{"types": entity.DetailEnum})
			}

			types = append(types, dt)
		}
	}

	q := ctx.Query("q")

	groups, err := r.uc.Search.Suggest(ctx.UserContext(), q, types, ctx.QueryInt("limit", entity.SearchGroupMax))
	if err != nil {
		return r.fail(ctx, "suggest", err)
	}

	out := response.Suggestions{Query: strings.TrimSpace(q), Groups: make([]response.SearchGroup, 0, len(groups))}
	for _, g := range groups {
		out.Groups = append(out.Groups, response.SearchGroup{Type: string(g.Type), Items: response.SearchItemsOf(g.Items)})
	}

	return ctx.JSON(response.Data[response.Suggestions]{Data: out})
}

// search godoc
//
//	@Summary Search results of one kind (results page)
//	@Tags    search
//	@Produce json
//	@Param   q      query    string true  "2–100 characters after trimming"
//	@Param   type   query    string true  "kind" Enums(channel,stream,category,vod)
//	@Param   cursor query    string false "nextCursor of the previous page"
//	@Param   limit  query    int    false "1–50" default(24)
//	@Success 200    {object} response.Data[response.SearchList]
//	@Failure 400    {object} response.Error "VALIDATION_FAILED (q = LENGTH, type = REQUIRED | ENUM, cursor = INVALID)"
//	@Router  /search [get]
func (r *routes) search(ctx *fiber.Ctx) error {
	t := entity.DocType(strings.ToLower(ctx.Query("type")))

	switch {
	case t == "":
		return invalid(ctx, map[string]string{"type": entity.DetailRequired})
	case !t.Valid():
		return invalid(ctx, map[string]string{"type": entity.DetailEnum})
	}

	page, err := r.uc.Search.Search(ctx.UserContext(), ctx.Query("q"), t, ctx.Query("cursor"), ctx.QueryInt("limit", search.DefaultLimit))
	if err != nil {
		return r.fail(ctx, "search", err)
	}

	return ctx.JSON(response.Data[response.SearchList]{Data: response.SearchList{
		Items: response.SearchItemsOf(page.Items), NextCursor: response.Cursor(page.NextCursor),
	}})
}

func historyOf(q entity.SearchQuery) response.SearchQuery {
	return response.SearchQuery{ID: q.ID, Query: q.Query, CreatedAt: q.CreatedAt.UTC()}
}

// history godoc
//
//	@Summary  The caller's recent searches (newest first, at most 10)
//	@Tags     search
//	@Produce  json
//	@Security BearerAuth
//	@Success  200 {object} response.Data[response.SearchHistory]
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Router   /me/search-history [get]
func (r *routes) history(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	list, err := r.uc.Search.History(ctx.UserContext(), userID)
	if err != nil {
		return r.fail(ctx, "history", err)
	}

	out := response.SearchHistory{Items: make([]response.SearchQuery, 0, len(list))}
	for _, q := range list {
		out.Items = append(out.Items, historyOf(q))
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(response.Data[response.SearchHistory]{Data: out})
}

// remember godoc
//
//	@Summary     Remember a search
//	@Description The same query (case-insensitive) moves to the top with the same id; only the 10 newest are kept.
//	@Tags        search
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     request.SearchHistory true "query"
//	@Success     201  {object} response.Data[response.SearchQuery]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (query = LENGTH)"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Router      /me/search-history [post]
func (r *routes) remember(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	var body request.SearchHistory
	if err = ctx.BodyParser(&body); err != nil {
		return invalid(ctx, map[string]string{_fieldBody: entity.DetailFormat})
	}

	q, err := r.uc.Search.Remember(ctx.UserContext(), userID, body.Query)
	if err != nil {
		return r.fail(ctx, "remember", err)
	}

	return ctx.Status(http.StatusCreated).JSON(response.Data[response.SearchQuery]{Data: historyOf(q)})
}

// forget godoc
//
//	@Summary  Remove one remembered search
//	@Tags     search
//	@Security BearerAuth
//	@Param    id path string true "entry id (uuid)"
//	@Success  204
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Router   /me/search-history/{id} [delete]
func (r *routes) forget(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	id, ok := idParam(ctx)
	if !ok {
		return ctx.SendStatus(http.StatusNoContent)
	}

	if err = r.uc.Search.Forget(ctx.UserContext(), userID, &id); err != nil {
		return r.fail(ctx, "forget", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// forgetAll godoc
//
//	@Summary  Clear the search history
//	@Tags     search
//	@Security BearerAuth
//	@Success  204
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Router   /me/search-history [delete]
func (r *routes) forgetAll(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	if err = r.uc.Search.Forget(ctx.UserContext(), userID, nil); err != nil {
		return r.fail(ctx, "forgetAll", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// remind godoc
//
//	@Summary     Ask to be reminded of an occurrence
//	@Description 201 for a new reminder, 200 when it existed. The reminder itself is sent by notification-svc (schedule.reminder_due.v1, 10 minutes before).
//	@Tags        schedules
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string           true "schedule id (uuid)"
//	@Param       body body     request.Reminder true "occurrence"
//	@Success     200  {object} response.Data[response.Reminder]
//	@Success     201  {object} response.Data[response.Reminder]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED (occurrenceAt = REQUIRED | FORMAT)"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Failure     404  {object} response.Error "NOT_FOUND (schedule, or not one of its occurrences)"
//	@Failure     409  {object} response.Error "INVALID_STATE (past occurrence or cancelled schedule)"
//	@Router      /schedules/{id}/reminders [post]
func (r *routes) remind(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	id, ok := idParam(ctx)
	if !ok {
		return notFound(ctx)
	}

	var body request.Reminder
	if err = ctx.BodyParser(&body); err != nil {
		return invalid(ctx, map[string]string{_fieldBody: entity.DetailFormat})
	}

	at, d := request.ParseInstant(body.OccurrenceAt)
	if d != "" {
		return invalid(ctx, map[string]string{"occurrenceAt": d})
	}

	rem, created, err := r.uc.Reminders.Remind(ctx.UserContext(), userID, id, at)
	if err != nil {
		return r.fail(ctx, "remind", err)
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	return ctx.Status(status).JSON(response.Data[response.Reminder]{Data: response.Reminder{
		ScheduleID: rem.ScheduleID, OccurrenceAt: rem.OccurrenceAt.UTC(), CreatedAt: rem.CreatedAt.UTC(),
	}})
}

// unremind godoc
//
//	@Summary  Cancel a reminder (always 204)
//	@Tags     schedules
//	@Security BearerAuth
//	@Param    id           path  string true "schedule id (uuid)"
//	@Param    occurrenceAt query string true "RFC 3339"
//	@Success  204
//	@Failure  400 {object} response.Error "VALIDATION_FAILED (occurrenceAt = REQUIRED | FORMAT)"
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Router   /schedules/{id}/reminders [delete]
func (r *routes) unremind(ctx *fiber.Ctx) error {
	userID, err := requireCaller(ctx)
	if userID == "" {
		return err
	}

	at, d := request.ParseInstant(ctx.Query("occurrenceAt"))
	if d != "" {
		return invalid(ctx, map[string]string{"occurrenceAt": d})
	}

	id, ok := idParam(ctx)
	if !ok {
		return ctx.SendStatus(http.StatusNoContent)
	}

	if err = r.uc.Reminders.Unremind(ctx.UserContext(), userID, id, at); err != nil {
		return r.fail(ctx, "unremind", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}
