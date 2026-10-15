package clients_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()

	_, err := w.Write([]byte(body))
	assert.NoError(t, err)
}

func TestEvidence(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Secret") != "s3cret" {
			w.WriteHeader(http.StatusForbidden)

			return
		}

		switch r.URL.Query().Get("messageId") {
		case "missing":
			w.WriteHeader(http.StatusNotFound)
		case "slow":
			time.Sleep(200 * time.Millisecond)
		default:
			write(t, w, `{"items":[{"messageId":"m","userId":"u","body":"hi","fragments":[],"createdAt":"2026-10-15T00:00:00Z","deleted":true}]}`)
		}
	}))
	t.Cleanup(srv.Close)

	c := clients.NewChat(srv.URL, "s3cret", time.Second)
	ctx := context.Background()

	items, err := c.Evidence(ctx, "s", "m", "", 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].Deleted)

	_, err = c.Evidence(ctx, "s", "missing", "", 10)
	require.ErrorIs(t, err, entity.ErrNotFound)

	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()

	_, err = c.Evidence(short, "s", "slow", "", 10)
	require.Error(t, err)

	_, err = clients.NewChat(srv.URL, "wrong", time.Second).Evidence(ctx, "s", "m", "", 10)
	require.ErrorIs(t, err, clients.ErrStatus)
}

func TestSnapshots(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v1/users":
			write(t, w, `{"items":[{"id":"u1","username":"an","displayName":"An","globalRole":"USER","status":"ACTIVE",
				"createdAt":"2026-10-15T00:00:00Z","updatedAt":"2026-10-15T01:00:00Z"}],"nextAfter":"u1"}`)
		case "/internal/v1/channels":
			write(t, w, `{"items":[{"id":"c1","ownerId":"u1","handle":"an","displayName":"An","avatarUrl":null,
				"status":"SUSPENDED","suspendedAt":"2026-10-15T00:00:00Z","updatedAt":"2026-10-15T01:00:00Z"}],"nextAfter":null}`)
		default:
			write(t, w, `{"items":[{"id":"s1","channelId":"c1","title":"t","status":"LIVE","endReason":null,
				"vodId":null,"vodDeleted":false,"startedAt":null,"endedAt":null}],"nextAfter":null}`)
		}
	}))
	t.Cleanup(srv.Close)

	s := clients.NewSnapshots(srv.URL, srv.URL, srv.URL, "x")
	ctx := context.Background()

	users, next, err := s.Users(ctx, "", 500)
	require.NoError(t, err)
	require.Equal(t, "u1", next)
	require.Equal(t, "an", *users[0].Row.Username)
	require.Equal(t, 1, users[0].UpdatedAt.Hour())

	chans, next, err := s.Channels(ctx, "", 500)
	require.NoError(t, err)
	require.Empty(t, next)
	require.Equal(t, "SUSPENDED", *chans[0].Row.Status)
	require.True(t, chans[0].Row.SetAvatar)

	streams, _, err := s.Streams(ctx, "", 500)
	require.NoError(t, err)
	require.Equal(t, "LIVE", *streams[0].Row.Status)
}
