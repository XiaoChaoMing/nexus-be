package v1

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/services/safety/internal/usecase"
)

// fileReport -.
//
//	@Summary     Report a user, channel, stream or chat message
//	@Description A second report of the same target while the first one is still open returns it with 200 (not counted). More than 10 reports an hour → 429 with details.retryAfterSec. For a MESSAGE the room's streamId is required; the message and up to 9 earlier messages of its author are kept as evidence (evidenceStatus UNAVAILABLE when chat-svc does not answer within 300 ms). Reporting oneself → 400 targetId=INVALID.
//	@Tags        reports
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     ReportRequest true "report"
//	@Success     201  {object} Data[ReportCreated]
//	@Success     200  {object} Data[ReportCreated] "open duplicate"
//	@Failure     400  {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401  {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     404  {object} ErrorResponse "NOT_FOUND"
//	@Failure     429  {object} ErrorResponse "RATE_LIMITED"
//	@Router      /reports [post]
func (r *routes) fileReport(ctx *fiber.Ctx) error {
	userID, err := caller(ctx)
	if err != nil {
		return err
	}

	var body ReportRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	rep, created, err := r.uc.Reports.File(ctx.UserContext(), userID, usecase.ReportInput{
		TargetType: body.TargetType, TargetID: body.TargetID, Reason: body.Reason, Note: body.Note, StreamID: body.StreamID,
	})
	if err != nil {
		return r.fail(ctx, "fileReport", err)
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	return ctx.Status(status).JSON(Data[ReportCreated]{Data: ReportCreated{ID: rep.ID, Status: rep.Status, CreatedAt: rep.CreatedAt}})
}

// listReports -.
//
//	@Summary     Admin report queue
//	@Description Oldest first. status is a CSV list (default OPEN,IN_REVIEW); targetType and reason accept CSV too.
//	@Tags        admin-reports
//	@Produce     json
//	@Security    BearerAuth
//	@Param       status     query    string false "CSV of OPEN, IN_REVIEW, RESOLVED, DISMISSED"
//	@Param       targetType query    string false "CSV of USER, CHANNEL, STREAM, MESSAGE"
//	@Param       reason     query    string false "CSV of SPAM, HARASSMENT, VIOLENCE, SENSITIVE, COPYRIGHT, OTHER"
//	@Param       cursor     query    string false "nextCursor of the previous page"
//	@Param       limit      query    int    false "1–100, default 20"
//	@Success     200        {object} Data[ReportList]
//	@Failure     400        {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401        {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403        {object} ErrorResponse "FORBIDDEN (not an admin)"
//	@Router      /admin/reports [get]
func (r *routes) listReports(ctx *fiber.Ctx) error {
	cur, limit, ok := pageQuery(ctx)
	if !ok {
		return validationFailed(ctx, map[string]string{"limit": _detailFormat})
	}

	p, err := r.uc.Queue.List(ctx.UserContext(), usecase.ReportQuery{
		Statuses: csv(ctx.Query("status")), TargetTypes: csv(ctx.Query("targetType")), Reasons: csv(ctx.Query("reason")),
		Cursor: cur, Limit: limit,
	})
	if err != nil {
		return r.fail(ctx, "listReports", err)
	}

	return ctx.JSON(Data[ReportList]{Data: ReportList{Items: adminReports(p.Items), NextCursor: cursor(p.Next)}})
}

// openCount -.
//
//	@Summary     Open report counts (admin menu badge)
//	@Tags        admin-reports
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} Data[OpenCount]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "FORBIDDEN"
//	@Router      /admin/reports/open-count [get]
func (r *routes) openCount(ctx *fiber.Ctx) error {
	open, inReview, err := r.uc.Queue.OpenCount(ctx.UserContext())
	if err != nil {
		return r.fail(ctx, "openCount", err)
	}

	return ctx.JSON(Data[OpenCount]{Data: OpenCount{Open: open, InReview: inReview}})
}

// getReport -.
//
//	@Summary     Report detail with evidence, related reports and admin actions
//	@Tags        admin-reports
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "report id"
//	@Success     200 {object} Data[AdminReportDetail]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "FORBIDDEN"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Router      /admin/reports/{id} [get]
func (r *routes) getReport(ctx *fiber.Ctx) error {
	d, err := r.uc.Queue.Get(ctx.UserContext(), ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "getReport", err)
	}

	return ctx.JSON(Data[AdminReportDetail]{Data: reportDetail(d)})
}

// claim -.
//
//	@Summary     Claim a report (IN_REVIEW, assigned to the caller; another admin may take it over)
//	@Tags        admin-reports
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "report id"
//	@Success     200 {object} Data[AdminReport]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "FORBIDDEN"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Failure     409 {object} ErrorResponse "INVALID_STATE (closed)"
//	@Router      /admin/reports/{id}/claim [post]
func (r *routes) claim(ctx *fiber.Ctx) error {
	adminID, err := caller(ctx)
	if err != nil {
		return err
	}

	v, err := r.uc.Queue.Claim(ctx.UserContext(), adminID, ctx.Params("id"))
	if err != nil {
		return r.fail(ctx, "claim", err)
	}

	return ctx.JSON(Data[AdminReport]{Data: adminReport(v)})
}

// resolve -.
//
//	@Summary     Resolve or dismiss a report
//	@Description applyToSameTarget closes every open report on the same target; each closed report emits safety.report_resolved.v1 (the reporter is notified). note is 10–500 characters.
//	@Tags        admin-reports
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string         true "report id"
//	@Param       body body     ResolveRequest true "decision"
//	@Success     200  {object} Data[Resolved]
//	@Failure     400  {object} ErrorResponse "VALIDATION_FAILED"
//	@Failure     401  {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403  {object} ErrorResponse "FORBIDDEN"
//	@Failure     404  {object} ErrorResponse "NOT_FOUND"
//	@Failure     409  {object} ErrorResponse "INVALID_STATE (closed)"
//	@Router      /admin/reports/{id}/resolve [post]
func (r *routes) resolve(ctx *fiber.Ctx) error {
	adminID, err := caller(ctx)
	if err != nil {
		return err
	}

	var body ResolveRequest
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	v, closed, err := r.uc.Queue.Resolve(ctx.UserContext(), adminID, ctx.Params("id"), usecase.ResolveInput{
		Status: body.Status, Note: body.Note, ApplyToSameTarget: body.ApplyToSameTarget,
	})
	if err != nil {
		return r.fail(ctx, "resolve", err)
	}

	return ctx.JSON(Data[Resolved]{Data: Resolved{Report: adminReport(v), ClosedCount: closed}})
}
