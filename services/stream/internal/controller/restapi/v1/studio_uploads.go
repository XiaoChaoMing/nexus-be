package v1

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/middleware"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

// Error codes of the upload endpoints.
const (
	CodeFileTooLarge       = "FILE_TOO_LARGE"
	CodeUnsupportedFormat  = "UNSUPPORTED_FORMAT"
	CodeUploadLimit        = "UPLOAD_LIMIT_REACHED"
	CodeUploadExpired      = "UPLOAD_EXPIRED"
	CodePartsMissing       = "PARTS_MISSING"
	CodeInvalidPart        = "INVALID_PART"
	CodeStorageUnavailable = "STORAGE_UNAVAILABLE"

	_maxFileName    = 255
	_maxContentType = 100
)

// UploadRequest is the body of POST /studio/uploads.
type UploadRequest struct {
	// FileName is shown to the creator only (never used as a path); .mp4, .mov, .mkv or .webm.
	FileName  string `json:"fileName"`
	SizeBytes int64  `json:"sizeBytes"`
	// ContentType is video/mp4, video/quicktime, video/x-matroska, video/webm, or empty / application/octet-stream
	// (the extension decides).
	ContentType string  `json:"contentType"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	CategoryID  *string `json:"categoryId"`
	IsMature    bool    `json:"isMature"`
	// Visibility defaults to PUBLIC.
	Visibility string `json:"visibility" enums:"PUBLIC,HIDDEN"`
}

func (b UploadRequest) validate() map[string]string {
	errs := map[string]string{}

	if n := utf8.RuneCountInString(b.FileName); strings.TrimSpace(b.FileName) == "" || n > _maxFileName {
		errs["fileName"] = _detailLength
	}

	if b.SizeBytes < 1 {
		errs["sizeBytes"] = "MIN"
	}

	if len(b.ContentType) > _maxContentType {
		errs["contentType"] = _detailLength
	}

	b.validateDetails(errs)

	return errs
}

// validateDetails checks what the video will show: title, description, category and visibility.
func (b UploadRequest) validateDetails(errs map[string]string) {
	if n := utf8.RuneCountInString(strings.TrimSpace(b.Title)); n == 0 || n > _maxTitle {
		errs["title"] = _detailLength
	}

	if utf8.RuneCountInString(b.Description) > _maxDescription {
		errs["description"] = _detailLength
	}

	if b.CategoryID != nil {
		if _, err := uuid.Parse(*b.CategoryID); err != nil {
			errs["categoryId"] = _detailInvalid
		}
	}

	if b.Visibility != "" && b.Visibility != entity.VisibilityPublic && b.Visibility != entity.VisibilityHidden {
		errs["visibility"] = _detailEnum
	}
}

// Upload is an upload session; uploadedParts lists the received part numbers in ascending order, so a client that
// lost its connection sends only the missing ones.
type Upload struct {
	ID            string    `json:"id"`
	Status        string    `json:"status" enums:"UPLOADING,COMPLETED,ABORTED,EXPIRED"`
	FileName      string    `json:"fileName"`
	SizeBytes     int64     `json:"sizeBytes"`
	PartSize      int64     `json:"partSize"`
	PartCount     int       `json:"partCount"`
	UploadedParts []int     `json:"uploadedParts"`
	VODID         *string   `json:"vodId"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// PartReceived answers PUT /studio/uploads/{id}/parts/{n}.
type PartReceived struct {
	PartNumber    int   `json:"partNumber"`
	ReceivedBytes int64 `json:"receivedBytes"`
}

// UploadCompleted answers POST /studio/uploads/{id}/complete.
type UploadCompleted struct {
	VODID  string `json:"vodId"`
	Upload Upload `json:"upload"`
}

func uploadBody(u entity.Upload) Upload {
	return Upload{
		ID: u.ID, Status: string(u.Status), FileName: u.FileName, SizeBytes: u.SizeBytes, PartSize: u.PartSize,
		PartCount: u.PartCount, UploadedParts: u.UploadedParts(), VODID: u.VODID, CreatedAt: u.CreatedAt.UTC(),
		ExpiresAt: u.ExpiresAt.UTC(),
	}
}

// requireUploads answers 503 on every upload route while uploads are turned off.
func (r *routes) requireUploads(ctx *fiber.Ctx) error {
	if !r.o.UploadsEnabled {
		return errorJSON(ctx, http.StatusServiceUnavailable, CodeStorageUnavailable, "video uploads are unavailable", nil)
	}

	return ctx.Next()
}

// uploadError maps the upload sentinels; storage failures are logged since the caller only sees 503.
func (r *routes) uploadError(ctx *fiber.Ctx, op string, err error) error {
	switch {
	case errors.Is(err, entity.ErrNotFound):
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, entity.ErrChannelSuspended):
		return channelSuspended(ctx)
	case errors.Is(err, entity.ErrUnsupportedFormat):
		return errorJSON(ctx, http.StatusUnprocessableEntity, CodeUnsupportedFormat, "unsupported video format", nil)
	case errors.Is(err, entity.ErrUploadExpired):
		return errorJSON(ctx, http.StatusGone, CodeUploadExpired, "upload expired or aborted", nil)
	case errors.Is(err, entity.ErrInvalidState):
		return errorJSON(ctx, http.StatusConflict, CodeInvalidState, "not allowed in the current state", nil)
	case errors.Is(err, entity.ErrStorageUnavailable):
		r.l.Error("restapi - v1 - %s: %v", op, err)

		return errorJSON(ctx, http.StatusServiceUnavailable, CodeStorageUnavailable, "storage unavailable", nil)
	}

	if handled, herr := uploadDetailError(ctx, err); handled {
		return herr
	}

	return r.internal(ctx, op, err)
}

// uploadDetailError answers the upload errors that carry details; handled is false for any other error.
func uploadDetailError(ctx *fiber.Ctx, err error) (handled bool, out error) {
	var (
		tooLarge *entity.FileTooLargeError
		limit    *entity.UploadLimitError
		missing  *entity.PartsMissingError
		badPart  *entity.InvalidPartError
	)

	switch {
	case errors.As(err, &tooLarge):
		return true, errorDetails(ctx, http.StatusRequestEntityTooLarge, CodeFileTooLarge, "file too large",
			map[string]any{"maxBytes": tooLarge.MaxBytes})
	case errors.As(err, &limit):
		sec := int64(limit.RetryAfter.Round(time.Second).Seconds())
		ctx.Set(fiber.HeaderRetryAfter, strconv.FormatInt(sec, 10))

		return true, errorDetails(ctx, http.StatusTooManyRequests, CodeUploadLimit, "upload limit reached",
			map[string]any{"limit": limit.Limit, "retryAfterSec": sec})
	case errors.As(err, &missing):
		return true, errorDetails(ctx, http.StatusConflict, CodePartsMissing, "upload parts missing",
			map[string]any{"missing": missing.Missing})
	case errors.As(err, &badPart):
		return true, errorDetails(ctx, http.StatusBadRequest, CodeInvalidPart, "wrong part size",
			map[string]any{"expectedBytes": badPart.ExpectedBytes})
	case errors.Is(err, entity.ErrInvalidPart):
		return true, errorJSON(ctx, http.StatusBadRequest, CodeInvalidPart, "invalid part number", nil)
	}

	return false, nil
}

func uploadID(ctx *fiber.Ctx) (id string, ok bool) {
	u, err := uuid.Parse(ctx.Params("id"))
	if err != nil {
		return "", false
	}

	return u.String(), true
}

// createUpload godoc
//
//	@Summary     Studio: start uploading a video file
//	@Description Opens a session that takes the file in parts of partSize bytes (PUT …/parts/{n}, 1…partCount) until expiresAt. At most 3 uploads in flight (uploading or processing) and 10 sessions a day per channel.
//	@Tags        studio-uploads
//	@Accept      json
//	@Produce     json
//	@Security    BearerAuth
//	@Param       body body     UploadRequest true "file and video details"
//	@Success     201  {object} Data[Upload]
//	@Failure     400  {object} ErrorResponse "VALIDATION_FAILED (fileName / contentType / title / description = LENGTH, sizeBytes = MIN, categoryId = INVALID, visibility = ENUM)"
//	@Failure     401  {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403  {object} ErrorResponse "NOT_CREATOR, CHANNEL_SUSPENDED"
//	@Failure     413  {object} ErrorResponse "FILE_TOO_LARGE (details.maxBytes)"
//	@Failure     422  {object} ErrorResponse "UNSUPPORTED_FORMAT"
//	@Failure     429  {object} ErrorResponse "UPLOAD_LIMIT_REACHED (details.limit = ACTIVE | DAILY, details.retryAfterSec)"
//	@Failure     503  {object} ErrorResponse "STORAGE_UNAVAILABLE"
//	@Router      /studio/uploads [post]
func (r *routes) createUpload(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	userID, ok := middleware.UserID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusUnauthorized, CodeUnauthorized, "unauthorized", nil)
	}

	var body UploadRequest
	if err := ctx.BodyParser(&body); err != nil {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", nil)
	}

	if errs := body.validate(); len(errs) > 0 {
		return errorJSON(ctx, http.StatusBadRequest, CodeValidationFailed, "invalid request body", errs)
	}

	visibility := body.Visibility
	if visibility == "" {
		visibility = entity.VisibilityPublic
	}

	u, err := r.uploads.Create(ctx.UserContext(), ch, usecase.NewUpload{
		UserID: userID, FileName: body.FileName, ContentType: body.ContentType, SizeBytes: body.SizeBytes,
		Title: strings.TrimSpace(body.Title), Description: body.Description, CategoryID: lowerID(body.CategoryID),
		IsMature: body.IsMature, Visibility: visibility,
	})
	if err != nil {
		return r.uploadError(ctx, "createUpload", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.Status(http.StatusCreated).JSON(Data[Upload]{Data: uploadBody(u)})
}

// putPart godoc
//
//	@Summary     Studio: send one part of the file
//	@Description The body is the part's bytes (application/octet-stream, at most 8 MiB): exactly partSize bytes for every part but the last, which carries the rest of the file. Sending a part again replaces it.
//	@Tags        studio-uploads
//	@Accept      octet-stream
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id   path     string true "upload id (uuid)"
//	@Param       n    path     int    true "part number, 1…partCount"
//	@Param       body body     string true "the part's bytes" format(binary)
//	@Success     200  {object} Data[PartReceived]
//	@Failure     400  {object} ErrorResponse "INVALID_PART (details.expectedBytes when the size is wrong)"
//	@Failure     401  {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403  {object} ErrorResponse "NOT_CREATOR, CHANNEL_SUSPENDED"
//	@Failure     404  {object} ErrorResponse "NOT_FOUND"
//	@Failure     409  {object} ErrorResponse "INVALID_STATE (COMPLETED)"
//	@Failure     410  {object} ErrorResponse "UPLOAD_EXPIRED (EXPIRED or ABORTED)"
//	@Failure     503  {object} ErrorResponse "STORAGE_UNAVAILABLE"
//	@Router      /studio/uploads/{id}/parts/{n} [put]
func (r *routes) putPart(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	id, ok := uploadID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "upload not found", nil)
	}

	n, err := strconv.Atoi(ctx.Params("n"))
	if err != nil {
		return errorJSON(ctx, http.StatusBadRequest, CodeInvalidPart, "invalid part number", nil)
	}

	// The raw body (never decompressed), bounded by the server's body limit.
	part := ctx.Request().Body()

	if err = r.uploads.PutPart(ctx.UserContext(), ch, id, n, bytes.NewReader(part), int64(len(part))); err != nil {
		return r.uploadError(ctx, "putPart", err)
	}

	return ctx.JSON(Data[PartReceived]{Data: PartReceived{PartNumber: n, ReceivedBytes: int64(len(part))}})
}

// getUpload godoc
//
//	@Summary  Studio: an upload session and the parts received so far
//	@Tags     studio-uploads
//	@Produce  json
//	@Security BearerAuth
//	@Param    id  path     string true "upload id (uuid)"
//	@Success  200 {object} Data[Upload]
//	@Failure  401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure  403 {object} ErrorResponse "NOT_CREATOR"
//	@Failure  404 {object} ErrorResponse "NOT_FOUND"
//	@Failure  503 {object} ErrorResponse "STORAGE_UNAVAILABLE (uploads turned off)"
//	@Router   /studio/uploads/{id} [get]
func (r *routes) getUpload(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	id, ok := uploadID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "upload not found", nil)
	}

	u, err := r.uploads.Get(ctx.UserContext(), ch.ID, id)
	if err != nil {
		return r.uploadError(ctx, "getUpload", err)
	}

	ctx.Set(fiber.HeaderCacheControl, "no-store")

	return ctx.JSON(Data[Upload]{Data: uploadBody(u)})
}

// completeUpload godoc
//
//	@Summary     Studio: finish the upload
//	@Description Assembles the parts and creates the video, PROCESSING until media-svc has transcoded it (follow it in GET /studio/vods). Calling it again on a COMPLETED session returns the same vodId.
//	@Tags        studio-uploads
//	@Produce     json
//	@Security    BearerAuth
//	@Param       id  path     string true "upload id (uuid)"
//	@Success     202 {object} Data[UploadCompleted]
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "NOT_CREATOR, CHANNEL_SUSPENDED"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Failure     409 {object} ErrorResponse "PARTS_MISSING (details.missing: part numbers)"
//	@Failure     410 {object} ErrorResponse "UPLOAD_EXPIRED"
//	@Failure     503 {object} ErrorResponse "STORAGE_UNAVAILABLE"
//	@Router      /studio/uploads/{id}/complete [post]
func (r *routes) completeUpload(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	id, ok := uploadID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "upload not found", nil)
	}

	u, err := r.uploads.Complete(ctx.UserContext(), ch, id)
	if err != nil {
		return r.uploadError(ctx, "completeUpload", err)
	}

	out := UploadCompleted{Upload: uploadBody(u)}
	if u.VODID != nil {
		out.VODID = *u.VODID
	}

	return ctx.Status(http.StatusAccepted).JSON(Data[UploadCompleted]{Data: out})
}

// abortUpload godoc
//
//	@Summary     Studio: cancel an upload
//	@Description Drops the parts received so far. A session already aborted or expired is 204 too.
//	@Tags        studio-uploads
//	@Security    BearerAuth
//	@Param       id path string true "upload id (uuid)"
//	@Success     204
//	@Failure     401 {object} ErrorResponse "UNAUTHORIZED"
//	@Failure     403 {object} ErrorResponse "NOT_CREATOR"
//	@Failure     404 {object} ErrorResponse "NOT_FOUND"
//	@Failure     409 {object} ErrorResponse "INVALID_STATE (COMPLETED: delete the video instead)"
//	@Failure     503 {object} ErrorResponse "STORAGE_UNAVAILABLE (uploads turned off)"
//	@Router      /studio/uploads/{id} [delete]
func (r *routes) abortUpload(ctx *fiber.Ctx) error {
	ch, ok := creatorChannel(ctx)
	if !ok {
		return notCreator(ctx)
	}

	id, ok := uploadID(ctx)
	if !ok {
		return errorJSON(ctx, http.StatusNotFound, CodeNotFound, "upload not found", nil)
	}

	if err := r.uploads.Abort(ctx.UserContext(), ch.ID, id); err != nil {
		return r.uploadError(ctx, "abortUpload", err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}
