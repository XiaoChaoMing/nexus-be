package v1_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	v1 "github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/stretchr/testify/require"
)

// Upload ids the fake answers with a specific error.
const (
	_upOK       = "0199d4a2-0000-7000-8000-00000000a001"
	_upMissing  = "0199d4a2-0000-7000-8000-00000000a404"
	_upExpired  = "0199d4a2-0000-7000-8000-00000000a410"
	_upParts    = "0199d4a2-0000-7000-8000-00000000a409"
	_upDone     = "0199d4a2-0000-7000-8000-00000000a200"
	_upStorage  = "0199d4a2-0000-7000-8000-00000000a503"
	_vodFailed  = "0199d4a2-0000-7000-8000-00000000b001"
	_vodLive    = "0199d4a2-0000-7000-8000-00000000b409"
	_partSize   = 8 << 20
	_uploadSize = _partSize + 100
)

func uploadErr(id string) error {
	switch id {
	case _upMissing:
		return entity.ErrNotFound
	case _upExpired:
		return entity.ErrUploadExpired
	case _upParts:
		return &entity.PartsMissingError{Missing: []int{2, 3}}
	case _upDone:
		return entity.ErrInvalidState
	case _upStorage:
		return entity.ErrStorageUnavailable
	}

	return nil
}

// fakeUploads answers by upload id (uploadErr); Create by file name.
type fakeUploads struct {
	parts []int64
}

func sampleUpload(id string) entity.Upload {
	return entity.Upload{
		ID: id, Status: entity.UploadUploading, FileName: "a.mp4", SizeBytes: _uploadSize, PartSize: _partSize, PartCount: 2,
		Parts: map[int]entity.UploadPart{2: {}, 1: {}}, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
}

func (*fakeUploads) Create(_ context.Context, ch entity.Channel, in usecase.NewUpload) (entity.Upload, error) {
	switch {
	case ch.Suspended:
		return entity.Upload{}, entity.ErrChannelSuspended
	case in.FileName == "big.mp4":
		return entity.Upload{}, &entity.FileTooLargeError{MaxBytes: 2 << 30}
	case in.FileName == "a.avi":
		return entity.Upload{}, entity.ErrUnsupportedFormat
	case in.FileName == "many.mp4":
		return entity.Upload{}, &entity.UploadLimitError{Limit: entity.UploadLimitDaily, RetryAfter: 90 * time.Second}
	case in.FileName == "down.mp4":
		return entity.Upload{}, entity.ErrStorageUnavailable
	}

	return sampleUpload(_upOK), nil
}

func (f *fakeUploads) PutPart(_ context.Context, _ entity.Channel, id string, n int, body io.Reader, size int64) error {
	if err := uploadErr(id); err != nil {
		return err
	}

	if n > 2 {
		return entity.ErrInvalidPart
	}

	if n == 1 && size != _partSize {
		return &entity.InvalidPartError{ExpectedBytes: _partSize}
	}

	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	f.parts = append(f.parts, int64(len(b)))

	return nil
}

func (*fakeUploads) Get(_ context.Context, _, id string) (entity.Upload, error) {
	if err := uploadErr(id); err != nil {
		return entity.Upload{}, err
	}

	return sampleUpload(id), nil
}

func (*fakeUploads) Complete(_ context.Context, _ entity.Channel, id string) (entity.Upload, error) {
	if err := uploadErr(id); err != nil {
		return entity.Upload{}, err
	}

	u := sampleUpload(id)
	vod := _vodFailed
	u.Status, u.VODID = entity.UploadCompleted, &vod

	return u, nil
}

func (*fakeUploads) Abort(_ context.Context, _, id string) error { return uploadErr(id) }

func (*fakeUploads) Retry(_ context.Context, _ entity.Channel, vodID string) (entity.VOD, error) {
	if vodID == _vodLive {
		return entity.VOD{}, entity.ErrInvalidState
	}

	stage := "PROBING"

	return entity.VOD{ID: vodID, Source: entity.SourceUpload, Status: entity.VODProcessing, ProcessingStage: &stage}, nil
}

func doRaw(t *testing.T, app *fiber.App, method, path, user, contentType string, body []byte,
) (status int, out []byte, hdr http.Header) {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User", user)

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	out, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, out, resp.Header
}

func uploadBody(name string) []byte {
	return []byte(`{"fileName":"` + name + `","sizeBytes":8388708,"contentType":"video/mp4","title":"Học Go","visibility":"PUBLIC"}`)
}

func TestUploadsDisabled(t *testing.T) {
	t.Parallel()

	app := newAppFull(&fakeStudio{}, &fakeViewer{}, &fakeUploads{}, v1.Options{HLSBase: "/hls"})

	for _, path := range []string{"/v1/studio/uploads", "/v1/studio/uploads/" + _upOK + "/complete"} {
		status, body, _ := doRaw(t, app, http.MethodPost, path, "creator", "application/json", uploadBody("a.mp4"))
		require.Equal(t, http.StatusServiceUnavailable, status)
		require.Contains(t, string(body), v1.CodeStorageUnavailable)
	}

	status, _, _ := doRaw(t, app, http.MethodPost, "/v1/studio/uploads", "nobody", "application/json", uploadBody("a.mp4"))
	require.Equal(t, http.StatusForbidden, status, "requireCreator runs first")
}

func TestCreateUpload(t *testing.T) {
	t.Parallel()

	app := newAppFull(&fakeStudio{}, &fakeViewer{}, &fakeUploads{}, v1.Options{HLSBase: "/hls", UploadsEnabled: true})

	for _, tc := range []struct {
		name, user string
		body       []byte
		status     int
		code       string
		details    map[string]any
	}{
		{name: "created", user: "creator", body: uploadBody("a.mp4"), status: http.StatusCreated},
		{
			name: "validation", user: "creator",
			body:   []byte(`{"fileName":"","sizeBytes":0,"title":" ","categoryId":"x","visibility":"PRIVATE"}`),
			status: http.StatusBadRequest, code: v1.CodeValidationFailed,
			details: map[string]any{
				"fileName": "LENGTH", "sizeBytes": "MIN", "title": "LENGTH", "categoryId": "INVALID", "visibility": "ENUM",
			},
		},
		{name: "suspended", user: "suspended", body: uploadBody("a.mp4"), status: http.StatusForbidden, code: v1.CodeChannelSuspended},
		{
			name: "too large", user: "creator", body: uploadBody("big.mp4"), status: http.StatusRequestEntityTooLarge,
			code: v1.CodeFileTooLarge, details: map[string]any{"maxBytes": float64(2 << 30)},
		},
		{name: "format", user: "creator", body: uploadBody("a.avi"), status: http.StatusUnprocessableEntity, code: v1.CodeUnsupportedFormat},
		{
			name: "limit", user: "creator", body: uploadBody("many.mp4"), status: http.StatusTooManyRequests, code: v1.CodeUploadLimit,
			details: map[string]any{"limit": "DAILY", "retryAfterSec": float64(90)},
		},
		{name: "storage", user: "creator", body: uploadBody("down.mp4"), status: http.StatusServiceUnavailable, code: v1.CodeStorageUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body, hdr := doRaw(t, app, http.MethodPost, "/v1/studio/uploads", tc.user, "application/json", tc.body)
			require.Equal(t, tc.status, status, string(body))

			if tc.code == "" {
				u := decode[v1.Data[v1.Upload]](t, body).Data
				require.Equal(t, []int{1, 2}, u.UploadedParts, "ascending")
				require.Equal(t, int64(_partSize), u.PartSize)

				return
			}

			e := decode[v1.ErrorResponse](t, body).Error
			require.Equal(t, tc.code, e.Code)

			if tc.details != nil {
				require.Equal(t, tc.details, e.Details)
			}

			if tc.code == v1.CodeUploadLimit {
				require.Equal(t, "90", hdr.Get("Retry-After"))
			}
		})
	}
}

func TestUploadParts(t *testing.T) {
	t.Parallel()

	up := &fakeUploads{}
	app := newAppFull(&fakeStudio{}, &fakeViewer{}, up, v1.Options{HLSBase: "/hls", UploadsEnabled: true})
	part := bytes.Repeat([]byte{1}, _partSize)

	status, body, _ := doRaw(t, app, http.MethodPut, "/v1/studio/uploads/"+_upOK+"/parts/1", "creator", "application/octet-stream", part)
	require.Equal(t, http.StatusOK, status, string(body))
	require.Equal(t, v1.PartReceived{PartNumber: 1, ReceivedBytes: _partSize}, decode[v1.Data[v1.PartReceived]](t, body).Data)
	require.Equal(t, []int64{_partSize}, up.parts, "the whole body reaches the use case")

	status, body, _ = doRaw(t, app, http.MethodPut, "/v1/studio/uploads/"+_upOK+"/parts/1", "creator", "application/octet-stream",
		[]byte{1})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, map[string]any{"expectedBytes": float64(_partSize)}, decode[v1.ErrorResponse](t, body).Error.Details)

	for _, tc := range []struct {
		name, path string
		status     int
		code       string
	}{
		{name: "number not an int", path: _upOK + "/parts/x", status: http.StatusBadRequest, code: v1.CodeInvalidPart},
		{name: "number out of range", path: _upOK + "/parts/3", status: http.StatusBadRequest, code: v1.CodeInvalidPart},
		{name: "other channel", path: _upMissing + "/parts/1", status: http.StatusNotFound, code: v1.CodeNotFound},
		{name: "bad id", path: "nope/parts/1", status: http.StatusNotFound, code: v1.CodeNotFound},
		{name: "expired", path: _upExpired + "/parts/1", status: http.StatusGone, code: v1.CodeUploadExpired},
		{name: "completed", path: _upDone + "/parts/1", status: http.StatusConflict, code: v1.CodeInvalidState},
		{name: "storage", path: _upStorage + "/parts/1", status: http.StatusServiceUnavailable, code: v1.CodeStorageUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body, _ := doRaw(t, app, http.MethodPut, "/v1/studio/uploads/"+tc.path, "creator", "application/octet-stream", nil)
			require.Equal(t, tc.status, status, string(body))
			require.Equal(t, tc.code, decode[v1.ErrorResponse](t, body).Error.Code)
		})
	}
}

func TestUploadSession(t *testing.T) {
	t.Parallel()

	app := newAppFull(&fakeStudio{}, &fakeViewer{}, &fakeUploads{}, v1.Options{HLSBase: "/hls", UploadsEnabled: true})

	for _, tc := range []struct {
		name, method, path string
		status             int
		code               string
	}{
		{name: "get", method: http.MethodGet, path: "/v1/studio/uploads/" + _upOK, status: http.StatusOK},
		{name: "get missing", method: http.MethodGet, path: "/v1/studio/uploads/" + _upMissing, status: http.StatusNotFound, code: v1.CodeNotFound},
		{name: "complete", method: http.MethodPost, path: "/v1/studio/uploads/" + _upOK + "/complete", status: http.StatusAccepted},
		{
			name: "complete with parts missing", method: http.MethodPost, path: "/v1/studio/uploads/" + _upParts + "/complete",
			status: http.StatusConflict, code: v1.CodePartsMissing,
		},
		{
			name: "complete expired", method: http.MethodPost, path: "/v1/studio/uploads/" + _upExpired + "/complete",
			status: http.StatusGone, code: v1.CodeUploadExpired,
		},
		{name: "abort", method: http.MethodDelete, path: "/v1/studio/uploads/" + _upOK, status: http.StatusNoContent},
		{
			name: "abort completed", method: http.MethodDelete, path: "/v1/studio/uploads/" + _upDone,
			status: http.StatusConflict, code: v1.CodeInvalidState,
		},
		{name: "retry", method: http.MethodPost, path: "/v1/studio/vods/" + _vodFailed + "/retry", status: http.StatusAccepted},
		{
			name: "retry live vod", method: http.MethodPost, path: "/v1/studio/vods/" + _vodLive + "/retry",
			status: http.StatusConflict, code: v1.CodeInvalidState,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body, _ := doRaw(t, app, tc.method, tc.path, "creator", "application/json", nil)
			require.Equal(t, tc.status, status, string(body))

			if tc.code != "" {
				require.Equal(t, tc.code, decode[v1.ErrorResponse](t, body).Error.Code)
			}
		})
	}

	_, body, _ := doRaw(t, app, http.MethodPost, "/v1/studio/uploads/"+_upParts+"/complete", "creator", "application/json", nil)
	require.Equal(t, map[string]any{"missing": []any{float64(2), float64(3)}}, decode[v1.ErrorResponse](t, body).Error.Details)

	_, body, _ = doRaw(t, app, http.MethodPost, "/v1/studio/uploads/"+_upOK+"/complete", "creator", "application/json", nil)
	require.Equal(t, _vodFailed, decode[v1.Data[v1.UploadCompleted]](t, body).Data.VODID)

	_, body, _ = doRaw(t, app, http.MethodPost, "/v1/studio/vods/"+_vodFailed+"/retry", "creator", "application/json", nil)
	v := decode[v1.Data[v1.StudioVOD]](t, body).Data
	require.NotNil(t, v.Processing, "processing is set while PROCESSING")
	require.Empty(t, v.ManifestURL)
	require.Nil(t, v.StreamID)
}
