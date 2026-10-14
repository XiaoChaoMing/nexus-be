package v1_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/stretchr/testify/require"
)

func (*fakeStudio) UpdateVOD(_ context.Context, _, vodID string, p entity.VODPatch) (entity.VOD, error) {
	if vodID == "0199d4a2-0000-7000-8000-0000000000ee" {
		return entity.VOD{}, entity.ErrInvalidState
	}

	return entity.VOD{ID: vodID, Status: entity.VODReady, Visibility: *p.Visibility}, nil
}

func (*fakeStudio) DeleteVOD(_ context.Context, _, vodID string) error {
	if vodID == "0199d4a2-0000-7000-8000-0000000000ee" {
		return entity.ErrNotFound
	}

	return nil
}

func TestGD4Validation(t *testing.T) { //nolint:funlen // one table of routes
	t.Parallel()

	app := newApp(&fakeStudio{})

	tests := []struct {
		name, method, path, body string
		status                   int
		details                  map[string]string
	}{
		{
			name: "category", method: http.MethodGet, path: "/v1/streams?category=lmht", status: http.StatusBadRequest,
			details: map[string]string{"category": "INVALID"},
		},
		{
			name: "language", method: http.MethodGet, path: "/v1/streams?language=vie", status: http.StatusBadRequest,
			details: map[string]string{"language": "FORMAT"},
		},
		{
			name: "tags", method: http.MethodGet, path: "/v1/streams?tags=a,b,c,d,e,f", status: http.StatusBadRequest,
			details: map[string]string{"tags": "TAG_COUNT"},
		},
		{
			name: "sort", method: http.MethodGet, path: "/v1/streams?sort=hot", status: http.StatusBadRequest,
			details: map[string]string{"sort": "ENUM"},
		},
		{
			name: "vod category", method: http.MethodGet, path: "/v1/vods?category=1", status: http.StatusBadRequest,
			details: map[string]string{"category": "INVALID"},
		},
		{name: "channel vods bad id", method: http.MethodGet, path: "/v1/channels/minhnn/vods", status: http.StatusNotFound},
		{
			name: "settings category", method: http.MethodPut, path: "/v1/studio/stream", body: `{"title":"x","categoryId":"lmht"}`,
			status: http.StatusBadRequest, details: map[string]string{"categoryId": "INVALID"},
		},
		{
			name: "patch empty", method: http.MethodPatch, path: "/v1/studio/vods/0199d4a2-0000-7000-8000-000000000001", body: `{}`,
			status: http.StatusBadRequest, details: map[string]string{"body": "REQUIRED"},
		},
		{
			name: "patch visibility", method: http.MethodPatch, path: "/v1/studio/vods/0199d4a2-0000-7000-8000-000000000001",
			body: `{"visibility":"PRIVATE","title":" "}`, status: http.StatusBadRequest,
			details: map[string]string{"visibility": "ENUM", "title": "LENGTH"},
		},
		{
			name: "patch expired", method: http.MethodPatch, path: "/v1/studio/vods/0199d4a2-0000-7000-8000-0000000000ee",
			body: `{"visibility":"HIDDEN"}`, status: http.StatusConflict,
		},
		{
			name: "hide", method: http.MethodPatch, path: "/v1/studio/vods/0199d4a2-0000-7000-8000-000000000001",
			body: `{"visibility":"HIDDEN"}`, status: http.StatusOK,
		},
		{name: "delete", method: http.MethodDelete, path: "/v1/studio/vods/0199d4a2-0000-7000-8000-000000000001", status: http.StatusNoContent},
		{name: "delete again", method: http.MethodDelete, path: "/v1/studio/vods/0199d4a2-0000-7000-8000-0000000000ee", status: http.StatusNotFound},
		{
			name: "dashboard range", method: http.MethodGet, path: "/v1/studio/dashboard?range=1y", status: http.StatusBadRequest,
			details: map[string]string{"range": "ENUM"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := doBody(t, app, tc.method, tc.path, "creator", tc.body)
			require.Equal(t, tc.status, status, string(body))

			if tc.details != nil {
				require.Equal(t, tc.details, decode[v1.ErrorResponse](t, body).Error.Details)
			}
		})
	}
}

func (*fakeStudio) Dashboard(_ context.Context, _ string, r entity.Range) (entity.Dashboard, error) {
	if _, ok := r.Duration(); !ok {
		return entity.Dashboard{}, entity.ErrInvalidState
	}

	return entity.Dashboard{Range: r}, nil
}

func doBody(t *testing.T, app interface {
	Test(req *http.Request, msTimeout ...int) (*http.Response, error)
}, method, path, user, body string,
) (status int, out []byte) {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User", user)

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	out, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, out
}
