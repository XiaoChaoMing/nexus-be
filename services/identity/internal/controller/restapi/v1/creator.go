package v1

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/request"
	"github.com/minhnn/nexuslive-be/services/identity/internal/controller/restapi/v1/response"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

const (
	_defaultPageSize = 20
	_statusPending   = "PENDING"
)

// applicationID reads :id; anything that is not a UUID cannot exist.
func applicationID(ctx *fiber.Ctx) (string, bool) {
	id := ctx.Params("id")
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}

	return id, true
}

func applicationNotFound(ctx *fiber.Ctx) error {
	return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "application not found", nil)
}

// submitApplication godoc
//
//	@Summary     Apply to become a creator
//	@Description Needs a verified e-mail, no pending application and no channel. legalName 2-100 characters,
//	@Description birthDate YYYY-MM-DD (age at least IDENTITY_CREATOR_MIN_AGE), note up to 500, acceptCreatorTerms true.
//	@Tags        creator
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     request.SubmitApplication true "Application"
//	@Success     201  {object} response.Data[response.Application]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Failure     403  {object} response.Error "EMAIL_NOT_VERIFIED"
//	@Failure     409  {object} response.Error "APPLICATION_PENDING / ALREADY_CREATOR"
//	@Failure     422  {object} response.Error "UNDERAGE"
//	@Router      /creator-applications [post]
func (r *V1) submitApplication(ctx *fiber.Ctx) error {
	userID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	var body request.SubmitApplication
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	body.LegalName = strings.TrimSpace(body.LegalName)
	body.Note = strings.TrimSpace(body.Note)

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	a, err := r.creator.Submit(ctx.UserContext(), usecase.SubmitInput{
		UserID: userID, LegalName: body.LegalName, BirthDate: body.BirthDate, Note: body.Note,
	})
	if err != nil {
		return r.mapError(ctx, "submitApplication", err)
	}

	return ctx.Status(http.StatusCreated).JSON(response.Data[response.Application]{Data: response.NewApplication(a)})
}

// myApplication godoc
//
//	@Summary     My latest creator application
//	@Description data is null when the user never applied.
//	@Tags        creator
//	@Produce     json
//	@Security    BearerAuth
//	@Success     200 {object} response.Data[response.Application]
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Router      /creator-applications/me [get]
func (r *V1) myApplication(ctx *fiber.Ctx) error {
	userID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	a, err := r.creator.Mine(ctx.UserContext(), userID)
	if err != nil {
		return r.mapError(ctx, "myApplication", err)
	}

	if a == nil {
		return ctx.JSON(response.Data[*response.Application]{Data: nil})
	}

	out := response.NewApplication(*a)

	return ctx.JSON(response.Data[*response.Application]{Data: &out})
}

// cancelApplication godoc
//
//	@Summary Cancel my pending application
//	@Tags    creator
//	@Security BearerAuth
//	@Param   id  path string true "Application id"
//	@Success 204
//	@Failure 401 {object} response.Error "UNAUTHORIZED"
//	@Failure 404 {object} response.Error "NOT_FOUND"
//	@Failure 409 {object} response.Error "INVALID_STATE (not pending)"
//	@Router  /creator-applications/{id} [delete]
func (r *V1) cancelApplication(ctx *fiber.Ctx) error {
	userID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	id, ok := applicationID(ctx)
	if !ok {
		return applicationNotFound(ctx)
	}

	if err := r.creator.Cancel(ctx.UserContext(), userID, id); err != nil {
		return r.mapError(ctx, "cancelApplication", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// listApplications godoc
//
//	@Summary     List creator applications (admin)
//	@Description Oldest first. Pass nextCursor back as cursor for the next page.
//	@Tags        admin
//	@Produce     json
//	@Security    BearerAuth
//	@Param       status query    string false "Status filter (default PENDING)" Enums(PENDING, APPROVED, REJECTED, CANCELLED, ALL)
//	@Param       cursor query    string false "Opaque cursor from nextCursor"
//	@Param       limit  query    int    false "Page size 1-50 (default 20)" minimum(1) maximum(50)
//	@Success     200    {object} response.Data[response.ApplicationPage]
//	@Failure     400    {object} response.Error "VALIDATION_FAILED"
//	@Failure     401    {object} response.Error "UNAUTHORIZED"
//	@Failure     403    {object} response.Error "FORBIDDEN"
//	@Router      /admin/creator-applications [get]
func (r *V1) listApplications(ctx *fiber.Ctx) error {
	var q request.ListApplications
	if err := ctx.QueryParser(&q); err != nil {
		return validationFailed(ctx, nil)
	}

	if err := r.v.Struct(q); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	status, limit := q.Status, _defaultPageSize
	if status == "" {
		status = _statusPending
	}

	if q.Limit != nil {
		limit = *q.Limit
	}

	page, err := r.creator.List(ctx.UserContext(), status, q.Cursor, limit)
	if err != nil {
		return r.mapError(ctx, "listApplications", err)
	}

	return ctx.JSON(response.Data[response.ApplicationPage]{Data: response.NewApplicationPage(page)})
}

// getApplication godoc
//
//	@Summary  Get a creator application (admin)
//	@Tags     admin
//	@Produce  json
//	@Security BearerAuth
//	@Param    id  path     string true "Application id"
//	@Success  200 {object} response.Data[response.AdminApplication]
//	@Failure  401 {object} response.Error "UNAUTHORIZED"
//	@Failure  403 {object} response.Error "FORBIDDEN"
//	@Failure  404 {object} response.Error "NOT_FOUND"
//	@Router   /admin/creator-applications/{id} [get]
func (r *V1) getApplication(ctx *fiber.Ctx) error {
	id, ok := applicationID(ctx)
	if !ok {
		return applicationNotFound(ctx)
	}

	a, err := r.creator.Get(ctx.UserContext(), id)

	return r.adminApplication(ctx, "getApplication", a, err)
}

// approveApplication godoc
//
//	@Summary     Approve a creator application (admin)
//	@Description PENDING only. Copies the birth date to the account; channel-svc then creates the channel.
//	@Tags        admin
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "Application id"
//	@Success     200 {object} response.Data[response.AdminApplication]
//	@Failure     401 {object} response.Error "UNAUTHORIZED"
//	@Failure     403 {object} response.Error "FORBIDDEN (not an admin, or own application)"
//	@Failure     404 {object} response.Error "NOT_FOUND"
//	@Failure     409 {object} response.Error "INVALID_STATE"
//	@Router      /admin/creator-applications/{id}/approve [post]
func (r *V1) approveApplication(ctx *fiber.Ctx) error {
	adminID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	id, ok := applicationID(ctx)
	if !ok {
		return applicationNotFound(ctx)
	}

	a, err := r.creator.Approve(ctx.UserContext(), adminID, id)

	return r.adminApplication(ctx, "approveApplication", a, err)
}

// rejectApplication godoc
//
//	@Summary     Reject a creator application (admin)
//	@Description PENDING only. The reason (10-500 characters, trimmed) is shown to the applicant.
//	@Tags        admin
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string                    true "Application id"
//	@Param       body body     request.RejectApplication true "Reason"
//	@Success     200  {object} response.Data[response.AdminApplication]
//	@Failure     400  {object} response.Error "VALIDATION_FAILED"
//	@Failure     401  {object} response.Error "UNAUTHORIZED"
//	@Failure     403  {object} response.Error "FORBIDDEN (not an admin, or own application)"
//	@Failure     404  {object} response.Error "NOT_FOUND"
//	@Failure     409  {object} response.Error "INVALID_STATE"
//	@Router      /admin/creator-applications/{id}/reject [post]
func (r *V1) rejectApplication(ctx *fiber.Ctx) error {
	adminID, ok := r.caller(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	id, ok := applicationID(ctx)
	if !ok {
		return applicationNotFound(ctx)
	}

	var body request.RejectApplication
	if err := ctx.BodyParser(&body); err != nil {
		return validationFailed(ctx, nil)
	}

	body.Reason = strings.TrimSpace(body.Reason)

	if err := r.v.Struct(body); err != nil {
		return validationFailed(ctx, fieldErrors(err))
	}

	a, err := r.creator.Reject(ctx.UserContext(), adminID, id, body.Reason)

	return r.adminApplication(ctx, "rejectApplication", a, err)
}

func (r *V1) adminApplication(ctx *fiber.Ctx, op string, a entity.Application, err error) error {
	if err != nil {
		return r.mapError(ctx, op, err)
	}

	return ctx.JSON(response.Data[response.AdminApplication]{Data: response.NewAdminApplication(a)})
}
