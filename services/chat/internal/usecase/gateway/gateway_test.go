package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/gateway"
	"github.com/stretchr/testify/require"
)

const (
	userID = "0199d4a2-0000-7000-8000-000000000001"
	stream = "0199d4a2-0000-7000-8000-0000000000aa"
)

var errBad = errors.New("bad")

type verifier struct{}

func (verifier) Verify(token string) (*jwt.Claims, error) {
	if token != "good" {
		return nil, errBad
	}

	return &jwt.Claims{Role: "ADMIN", RegisteredClaims: jwtlib.RegisteredClaims{
		Subject: userID, ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(time.Minute)),
	}}, nil
}

type viewers struct {
	usecase.Viewers
	joined []string
}

func (v *viewers) Join(_ context.Context, _, key string) (int, error) {
	v.joined = append(v.joined, key)

	return len(v.joined), nil
}

type presence struct {
	repo.PresenceStore
	conns int
}

func (p *presence) AddConn(context.Context, string, string, int) (bool, error) {
	p.conns++

	return p.conns <= 1, nil
}

func (*presence) StreamStatus(context.Context, string) (string, error) { return "LIVE", nil }

func TestAuthorizeAndJoin(t *testing.T) {
	t.Parallel()

	v, pr := &viewers{}, &presence{}
	uc := gateway.New(gateway.Deps{Verifier: verifier{}, Viewers: v, Presence: pr}, 1, logger.New("error"))
	ctx := context.Background()

	guest, err := uc.Authorize(ctx, nil, "0199D4A2-0000-7000-8000-00000000A0A0")
	require.NoError(t, err)
	require.True(t, guest.Guest())
	require.Equal(t, "0199d4a2-0000-7000-8000-00000000a0a0", guest.AnonID)

	guest, err = uc.Authorize(ctx, nil, "not-a-uuid")
	require.NoError(t, err)
	require.Empty(t, guest.AnonID, "ignored, counted per connection")

	bad := "bad"
	_, err = uc.Authorize(ctx, &bad, "")
	require.ErrorIs(t, err, entity.ErrInvalidToken)

	good := "good"
	p, err := uc.Authorize(ctx, &good, "")
	require.NoError(t, err)
	require.Equal(t, userID, p.UserID)
	require.True(t, p.IsAdmin())
	require.False(t, p.ExpiresAt.IsZero())

	require.NoError(t, uc.ConnOpened(ctx, p))
	require.ErrorIs(t, uc.ConnOpened(ctx, p), entity.ErrTooManyConnections)
	require.NoError(t, uc.ConnOpened(ctx, guest), "guests are not limited per user")

	_, err = uc.Join(ctx, guest, entity.Topic{Kind: entity.TopicUser, ID: userID})
	require.ErrorIs(t, err, entity.ErrForbidden)

	_, err = uc.Join(ctx, p, entity.Topic{Kind: entity.TopicUser, ID: userID})
	require.NoError(t, err)

	snap, err := uc.Join(ctx, p, entity.StreamTopic(stream))
	require.NoError(t, err)

	s, ok := snap.(usecase.StreamSnapshot)
	require.True(t, ok)
	require.Equal(t, "LIVE", *s.Status)
	require.Equal(t, 1, s.ViewerCount)
	require.Equal(t, []string{"u:" + userID}, v.joined)
}

type audience struct {
	repo.AudienceStore
	following []string
}

func (a audience) Following(context.Context, string) ([]string, error) { return a.following, nil }

type live map[string]entity.LiveInfo

func (l live) SetLive(context.Context, string, entity.LiveInfo) error { return nil }
func (l live) ClearLive(context.Context, string, string) error        { return nil }

func (l live) Live(_ context.Context, ids []string) (map[string]entity.LiveInfo, error) {
	out := map[string]entity.LiveInfo{}

	for _, id := range ids {
		if v, ok := l[id]; ok {
			out[id] = v
		}
	}

	return out, nil
}

type counts struct{ presence }

func (counts) ViewerCounts(_ context.Context, ids []string) (map[string]int, error) {
	out := map[string]int{}
	for _, id := range ids {
		out[id] = 12
	}

	return out, nil
}

func TestSidebar(t *testing.T) {
	t.Parallel()

	uc := gateway.New(gateway.Deps{
		Verifier: verifier{}, Viewers: &viewers{}, Presence: &counts{},
		Audience: audience{following: []string{"ch-live", "ch-off"}}, Live: live{"ch-live": {StreamID: "s1"}},
	}, 1, logger.New("error"))
	ctx := context.Background()

	good := "good"
	p, err := uc.Authorize(ctx, &good, "")
	require.NoError(t, err)

	_, err = uc.Join(ctx, p, entity.SidebarTopic(userID))
	require.NoError(t, err)

	_, err = uc.Join(ctx, p, entity.SidebarTopic(stream))
	require.ErrorIs(t, err, entity.ErrForbidden, "another user's sidebar")

	_, err = uc.Join(ctx, entity.Principal{AnonID: "x"}, entity.SidebarTopic(userID))
	require.ErrorIs(t, err, entity.ErrForbidden, "guests have no sidebar topic")

	items, err := uc.SidebarCounts(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, []entity.SidebarCount{{ChannelID: "ch-live", StreamID: "s1", Count: 12}}, items)
}

type locks struct{ locked bool }

func (l locks) Locked(context.Context, string) (bool, error) { return l.locked, nil }
func (locks) Lock(context.Context, string, *time.Time) error { return nil }
func (locks) Unlock(context.Context, string) error           { return nil }

func TestAuthorizeLockedAccount(t *testing.T) {
	t.Parallel()

	good := "good"

	locked := gateway.New(gateway.Deps{Verifier: verifier{}, Locks: locks{locked: true}}, 1, logger.New("error"))
	_, err := locked.Authorize(context.Background(), &good, "")
	require.ErrorIs(t, err, entity.ErrAccountLocked, "a valid token of a locked account is refused (close 4003)")

	open := gateway.New(gateway.Deps{Verifier: verifier{}, Locks: locks{}}, 1, logger.New("error"))
	p, err := open.Authorize(context.Background(), &good, "")
	require.NoError(t, err)
	require.Equal(t, userID, p.UserID)
}
