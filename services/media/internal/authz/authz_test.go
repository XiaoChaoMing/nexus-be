package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/minhnn/nexuslive-be/services/media/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestStatic(t *testing.T) {
	t.Parallel()

	a := NewStatic("live_secret")

	g, err := a.Authorize(context.Background(), "live_secret", "1.2.3.4")
	require.NoError(t, err)
	require.Equal(t, StaticChannelID, g.ChannelID)

	_, err = a.Authorize(context.Background(), "live_secreT", "")
	require.ErrorIs(t, err, entity.ErrUnauthorized)

	_, err = NewStatic("").Authorize(context.Background(), "", "")
	require.ErrorIs(t, err, entity.ErrUnauthorized, "empty configured key must reject everything")
}

func TestHTTP(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Secret") != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		switch body["streamKey"] {
		case "live_ok":
			fmt.Fprint(w, `{"streamId":"0199d4a2-0000-7000-8000-000000000001","channelId":"c1","resumed":true}`)
		case "live_busy":
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":{"code":"ALREADY_LIVE"}}`)
		case "live_cooldown":
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"error":{"code":"RECENTLY_ENDED"}}`)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	a := NewHTTP(srv.URL, "s3cret")

	g, err := a.Authorize(context.Background(), "live_ok", "1.2.3.4")
	require.NoError(t, err)
	require.True(t, g.Resumed)
	require.Equal(t, "c1", g.ChannelID)

	_, err = a.Authorize(context.Background(), "live_busy", "")
	require.ErrorIs(t, err, entity.ErrAlreadyLive)

	_, err = a.Authorize(context.Background(), "live_cooldown", "")
	require.ErrorIs(t, err, entity.ErrRecentlyEnded)

	_, err = a.Authorize(context.Background(), "nope", "")
	require.ErrorIs(t, err, entity.ErrUnauthorized)

	_, err = NewHTTP(srv.URL, "wrong").Authorize(context.Background(), "live_ok", "")
	require.Error(t, err)
	require.NotErrorIs(t, err, entity.ErrUnauthorized)
}
