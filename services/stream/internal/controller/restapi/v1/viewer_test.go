package v1_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/minhnn/nexuslive-be/services/stream/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
	"github.com/stretchr/testify/require"
)

const (
	_vodID    = "0199d4a2-0000-7000-8000-0000000000a1"
	_streamID = "0199d4a2-0000-7000-8000-000000000001"
	_matureID = "0199d4a2-0000-7000-8000-0000000000ff"
	_missing  = "0199d4a2-0000-7000-8000-000000000404"
	_anon     = "0199d4a2-0000-7000-8000-0000000000aa"
)

// fakeViewer: _matureID needs acknowledgment, _missing does not exist, every VOD is at 42 s for "viewer".
type fakeViewer struct {
	opened []usecase.PlaybackInput
	saved  []entity.WatchProgress
	keep   []bool
}

func (f *fakeViewer) Open(_ context.Context, in usecase.PlaybackInput) (entity.PlaybackGrant, error) {
	f.opened = append(f.opened, in)

	id := in.Target.StreamID + in.Target.VODID
	if id == _missing {
		return entity.PlaybackGrant{}, entity.ErrNotFound
	}

	g := entity.PlaybackGrant{AnonID: in.AnonID}
	if in.UserID == "" && g.AnonID == "" {
		g.AnonID = "generated"
	}

	if id == _matureID {
		g.Mature = entity.MatureGate{Required: true, Acknowledged: in.AcknowledgeMature}
		if !in.AcknowledgeMature {
			return g, nil
		}
	}

	g.Session = &entity.PlaybackSession{ID: "s1", ExpiresAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	g.ObjectKey = "streams/" + id + "/master.m3u8"

	return g, nil
}

func (f *fakeViewer) SaveProgress(_ context.Context, p entity.WatchProgress, keepFurther bool) error {
	switch {
	case p.VODID == _missing:
		return entity.ErrNotFound
	case p.PositionSec < 0:
		return entity.ErrInvalidPosition
	}

	f.saved = append(f.saved, p)
	f.keep = append(f.keep, keepFurther)

	return nil
}

func (*fakeViewer) Progress(_ context.Context, userID, _ string) (*int, error) {
	if userID != "viewer" {
		return nil, nil //nolint:nilnil // never watched
	}

	pos := 42

	return &pos, nil
}

func (*fakeViewer) ContinueWatching(_ context.Context, _ string, _ int) ([]entity.ContinueItem, error) {
	return []entity.ContinueItem{{
		VOD:         entity.VOD{ID: _vodID, PlaylistPath: "streams/x/vod.m3u8", ExpiresAt: time.Now()},
		PositionSec: 42, UpdatedAt: time.Now(),
	}}, nil
}

func (*fakeViewer) PurgeSessions(context.Context) (int64, error) { return 0, nil }

func (fakeCatalog) GetVOD(_ context.Context, id string) (entity.VOD, error) {
	if id == _missing {
		return entity.VOD{}, entity.ErrNotFound
	}

	return entity.VOD{ID: id, PlaylistPath: "streams/x/vod.m3u8"}, nil
}

func send(t *testing.T, newReq func() *http.Request, app interface {
	Test(*http.Request, ...int) (*http.Response, error)
},
) (status int, body []byte) {
	t.Helper()

	resp, err := app.Test(newReq())
	require.NoError(t, err)

	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, body
}

func req(method, path, user, anon, body string) func() *http.Request {
	return func() *http.Request {
		var rd io.Reader = http.NoBody
		if body != "" {
			rd = strings.NewReader(body)
		}

		r := httptest.NewRequestWithContext(context.Background(), method, path, rd)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}

		if user != "" {
			r.Header.Set("X-User", user)
		}

		if anon != "" {
			r.Header.Set(v1.HeaderAnonID, anon)
		}

		return r
	}
}

func TestPlayback(t *testing.T) {
	t.Parallel()

	viewer := &fakeViewer{}
	app := newAppWith(&fakeStudio{}, viewer)

	// Guest without an id gets one back, plus the manifest.
	status, body := send(t, req(http.MethodPost, "/v1/streams/"+_streamID+"/playback", "", "", ""), app)
	require.Equal(t, http.StatusOK, status)

	p := decode[v1.Data[v1.Playback]](t, body).Data
	require.NotNil(t, p.ManifestURL)
	require.Equal(t, "/hls/streams/"+_streamID+"/master.m3u8", *p.ManifestURL)
	require.NotNil(t, p.AnonID)
	require.Equal(t, "generated", *p.AnonID)
	require.False(t, p.Mature.Required)

	// Mature without acknowledgment: no media.
	status, body = send(t, req(http.MethodPost, "/v1/streams/"+_matureID+"/playback", "", _anon, ""), app)
	require.Equal(t, http.StatusOK, status)

	p = decode[v1.Data[v1.Playback]](t, body).Data
	require.True(t, p.Mature.Required)
	require.Nil(t, p.ManifestURL)
	require.Nil(t, p.SessionID)
	require.Contains(t, string(body), `"manifestUrl":null`)

	// Acknowledged, signed in: no anonId in the answer.
	status, body = send(t, req(http.MethodPost, "/v1/streams/"+_matureID+"/playback", "viewer", "", `{"acknowledgeMature":true}`), app)
	require.Equal(t, http.StatusOK, status)

	p = decode[v1.Data[v1.Playback]](t, body).Data
	require.NotNil(t, p.ManifestURL)
	require.Nil(t, p.AnonID)
	require.Equal(t, "viewer", viewer.opened[len(viewer.opened)-1].UserID)

	// VOD target.
	status, _ = send(t, req(http.MethodPost, "/v1/vods/"+_vodID+"/playback", "", _anon, ""), app)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, _vodID, viewer.opened[len(viewer.opened)-1].Target.VODID)
	require.Equal(t, _anon, viewer.opened[len(viewer.opened)-1].AnonID)

	for _, tc := range []struct {
		name, path, anon, body string
		want                   int
	}{
		{name: "missing", path: "/v1/streams/" + _missing + "/playback", want: http.StatusNotFound},
		{name: "not a uuid", path: "/v1/vods/abc/playback", want: http.StatusNotFound},
		{name: "bad anon id", path: "/v1/streams/" + _streamID + "/playback", anon: "x", want: http.StatusBadRequest},
		{name: "bad body", path: "/v1/streams/" + _streamID + "/playback", body: "{", want: http.StatusBadRequest},
	} {
		status, _ = send(t, req(http.MethodPost, tc.path, "", tc.anon, tc.body), app)
		require.Equal(t, tc.want, status, tc.name)
	}
}

func TestWatchProgress(t *testing.T) {
	t.Parallel()

	viewer := &fakeViewer{}
	app := newAppWith(&fakeStudio{}, viewer)
	path := "/v1/me/watch-progress/" + _vodID

	status, _ := send(t, req(http.MethodPut, path, "viewer", "", `{"positionSec":120,"keepFurther":true}`), app)
	require.Equal(t, http.StatusNoContent, status)
	require.Equal(t, 120, viewer.saved[0].PositionSec)
	require.Equal(t, "viewer", viewer.saved[0].UserID)
	require.True(t, viewer.keep[0])

	for _, tc := range []struct {
		name, path, user, body, code string
		want                         int
	}{
		{name: "guest", path: path, body: `{"positionSec":1}`, want: http.StatusUnauthorized},
		{name: "missing position", path: path, user: "viewer", body: `{}`, want: http.StatusBadRequest, code: "REQUIRED"},
		{name: "negative", path: path, user: "viewer", body: `{"positionSec":-1}`, want: http.StatusBadRequest, code: "RANGE"},
		{name: "unknown vod", path: "/v1/me/watch-progress/" + _missing, user: "viewer", body: `{"positionSec":1}`, want: http.StatusNotFound},
	} {
		status, body := send(t, req(http.MethodPut, tc.path, tc.user, "", tc.body), app)
		require.Equal(t, tc.want, status, tc.name)

		if tc.code != "" {
			require.Equal(t, tc.code, decode[v1.ErrorResponse](t, body).Error.Details["positionSec"], tc.name)
		}
	}

	status, body := send(t, req(http.MethodGet, "/v1/me/continue-watching", "viewer", "", ""), app)
	require.Equal(t, http.StatusOK, status)

	list := decode[v1.Data[v1.ContinueList]](t, body).Data
	require.Len(t, list.Items, 1)
	require.Equal(t, 42, list.Items[0].PositionSec)
	require.Equal(t, "/hls/streams/x/vod.m3u8", list.Items[0].VOD.ManifestURL)

	status, _ = send(t, req(http.MethodGet, "/v1/me/continue-watching", "", "", ""), app)
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestVODViewerField(t *testing.T) {
	t.Parallel()

	app := newAppWith(&fakeStudio{}, &fakeViewer{})

	status, body := send(t, req(http.MethodGet, "/v1/vods/"+_vodID, "viewer", "", ""), app)
	require.Equal(t, http.StatusOK, status)

	v := decode[v1.Data[v1.VOD]](t, body).Data
	require.NotNil(t, v.Viewer)
	require.NotNil(t, v.Viewer.PositionSec)
	require.Equal(t, 42, *v.Viewer.PositionSec)

	status, body = send(t, req(http.MethodGet, "/v1/vods/"+_vodID, "", "", ""), app)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, string(body), `"viewer"`, "guests get no viewer block")

	status, body = send(t, req(http.MethodGet, "/v1/vods/"+_vodID, "other", "", ""), app)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(body), `"viewer":{"positionSec":null}`)
}
