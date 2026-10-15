package v1_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	v1 "github.com/minhnn/nexuslive-be/services/chat/internal/controller/restapi/v1"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/stretchr/testify/require"
)

const (
	_channelID = "0199d4a2-0000-7000-8000-0000000000cc"
	_target    = "0199d4a2-0000-7000-8000-000000000002"
	_mod       = "0199d4a2-0000-7000-8000-000000000003"
)

// fakeMod answers by target: _mod is protected, the channel itself refuses non-moderators ("viewer").
type fakeMod struct {
	usecase.Moderation
	usecase.Roles
	gotDuration time.Duration
}

func (f *fakeMod) Timeout(_ context.Context, p entity.Principal, _, userID string, d time.Duration, _ *string) (entity.ActiveRestriction, error) {
	f.gotDuration = d

	switch {
	case p.UserID == "viewer":
		return entity.ActiveRestriction{}, entity.ErrForbidden
	case userID == _mod:
		return entity.ActiveRestriction{}, entity.Protected()
	case userID == p.UserID:
		return entity.ActiveRestriction{}, entity.SelfTarget()
	}

	return entity.ActiveRestriction{User: entity.UserRef{ID: userID, Username: "u"}, Type: entity.RestrictTimeout, CreatedAt: time.Now()}, nil
}

func (*fakeMod) Ban(context.Context, entity.Principal, string, string, *string) (entity.ActiveRestriction, error) {
	return entity.ActiveRestriction{}, entity.ErrInvalidState
}

func (*fakeMod) AddTerm(_ context.Context, _ entity.Principal, _, term string) (entity.BlockedTerm, bool, error) {
	if term == "full" {
		return entity.BlockedTerm{}, false, entity.Reject(entity.ErrLimitReached, "limit", "100")
	}

	return entity.BlockedTerm{ID: "t1", Display: term}, term != "again", nil
}

func (*fakeMod) Assign(_ context.Context, _ entity.Principal, _ string, who usecase.RoleTarget, role entity.ChannelRole,
) (entity.RoleGrant, bool, error) {
	return entity.RoleGrant{User: entity.UserRef{ID: _target, Username: who.Username}, Role: role}, true, nil
}

func newModApp(m *fakeMod) *fiber.App {
	app := fiber.New()
	auth := func(ctx *fiber.Ctx) error {
		if u := ctx.Get("X-User"); u != "" {
			ctx.Locals("userID", u)

			return ctx.Next()
		}

		return ctx.Status(http.StatusUnauthorized).JSON(fiber.Map{"error": fiber.Map{"code": "UNAUTHORIZED"}})
	}

	v1.NewRoutes(app.Group("/v1"), v1.UseCases{Chat: &fakeChat{}, Moderation: m, Roles: m}, v1.Auth{Required: auth, Optional: auth},
		logger.New("error"))

	return app
}

func TestModerationRoutes(t *testing.T) {
	t.Parallel()

	m := &fakeMod{}
	app := newModApp(m)
	base := "/v1/channels/" + _channelID

	for _, tc := range []struct {
		name, method, path, body, user string
		status                         int
		code                           string
		details                        map[string]any
	}{
		{name: "guest", method: http.MethodPut, path: base + "/timeouts/" + _target, body: `{"durationSec":60}`, status: 401, code: "UNAUTHORIZED"},
		{name: "viewer", method: http.MethodPut, path: base + "/timeouts/" + _target, body: `{"durationSec":60}`, user: "viewer", status: 403, code: "FORBIDDEN"},
		{
			name: "protected", method: http.MethodPut, path: base + "/timeouts/" + _mod, body: `{"durationSec":60}`, user: "m", status: 403,
			code: "FORBIDDEN", details: map[string]any{"target": "PROTECTED"},
		},
		{
			name: "self", method: http.MethodPut, path: base + "/timeouts/" + _target, body: `{"durationSec":60}`, user: _target, status: 400,
			code: "VALIDATION_FAILED", details: map[string]any{"userId": "INVALID"},
		},
		{
			name: "no duration", method: http.MethodPut, path: base + "/timeouts/" + _target, body: `{}`, user: "m", status: 400,
			code: "VALIDATION_FAILED", details: map[string]any{"durationSec": "REQUIRED"},
		},
		{name: "timeout", method: http.MethodPut, path: base + "/timeouts/" + _target, body: `{"durationSec":600}`, user: "m", status: 200},
		{name: "ban while banned", method: http.MethodPut, path: base + "/bans/" + _target, user: "m", status: 409, code: "INVALID_STATE"},
		{name: "bad user id", method: http.MethodPut, path: base + "/bans/x", user: "m", status: 404, code: "NOT_FOUND"},
		{name: "term created", method: http.MethodPost, path: base + "/blocked-terms", body: `{"term":"spam"}`, user: "m", status: 201},
		{name: "term again", method: http.MethodPost, path: base + "/blocked-terms", body: `{"term":"again"}`, user: "m", status: 200},
		{
			name: "term limit", method: http.MethodPost, path: base + "/blocked-terms", body: `{"term":"full"}`, user: "m", status: 409,
			code: "CONFLICT", details: map[string]any{"limit": "100"},
		},
		{
			name: "role bad", method: http.MethodPost, path: base + "/roles", body: `{"username":"a b","role":"BOSS"}`, user: "m", status: 400,
			code: "VALIDATION_FAILED", details: map[string]any{"username": "INVALID", "role": "ENUM"},
		},
		{name: "role", method: http.MethodPost, path: base + "/roles", body: `{"username":"an_1","role":"MODERATOR"}`, user: "m", status: 201},
		{
			name: "restrictions bad type", method: http.MethodGet, path: base + "/restrictions?type=KICK", user: "m", status: 400,
			code: "VALIDATION_FAILED", details: map[string]any{"type": "ENUM"},
		},
	} {
		status, body := do(t, app, tc.method, tc.path, tc.body, tc.user)
		require.Equal(t, tc.status, status, tc.name)

		if tc.code == "" {
			continue
		}

		e, ok := body["error"].(map[string]any)
		require.True(t, ok, tc.name)
		require.Equal(t, tc.code, e["code"], tc.name)

		if tc.details != nil {
			require.Equal(t, tc.details, e["details"], tc.name)
		}
	}

	require.Equal(t, 600*time.Second, m.gotDuration)
}
