package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	uc       *auth.UseCase
	store    *memStore
	sessions memSessions
	signer   *jwt.EdDSA
	clock    *time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	signer, err := jwt.GenerateEdDSA("test", 15*time.Minute)
	require.NoError(t, err)

	store := newMemStore()
	sessions := memSessions{store}
	// Cheap argon2 parameters keep the suite fast; production uses DefaultArgon2Params.
	hasher := auth.NewArgon2(auth.Argon2Params{Memory: 64, Time: 1, Threads: 1})

	uc, err := auth.New(auth.Repos{Users: memUsers{store}, Sessions: sessions, Tokens: memTokens{store}},
		hasher, signer, auth.Config{RefreshTTL: 24 * time.Hour}, logger.New("error"))
	require.NoError(t, err)

	// Real wall time: access tokens are verified against time.Now by the jwt library.
	clock := time.Now().UTC().Truncate(time.Second)
	f := &fixture{store: store, sessions: sessions, signer: signer, clock: &clock}
	f.uc = uc.WithClock(func() time.Time { return *f.clock })

	return f
}

var _client = usecase.ClientInfo{UserAgent: "test", IP: "127.0.0.1"} //nolint:gochecknoglobals // test fixture

func (f *fixture) register(t *testing.T) usecase.AuthResult {
	t.Helper()

	res, err := f.uc.Register(context.Background(), usecase.RegisterInput{
		Username: "viewer_1", Email: "v1@example.com", Password: "secret123", Client: _client,
	})
	require.NoError(t, err)

	return res
}

func TestRegister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		email    string
		wantErr  error
	}{
		{name: "new account", username: "viewer_2", email: "v2@example.com"},
		{name: "username taken, other case", username: "VIEWER_1", email: "x@example.com", wantErr: entity.ErrUsernameTaken},
		{name: "email taken, other case", username: "someone", email: "V1@EXAMPLE.COM", wantErr: entity.ErrEmailTaken},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.register(t)

			res, err := f.uc.Register(context.Background(), usecase.RegisterInput{
				Username: tc.username, Email: tc.email, Password: "secret123", Client: _client,
			})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.username, res.User.DisplayName, "display name defaults to username")
			assert.Equal(t, entity.DefaultLocale, res.User.Locale)
			assert.Equal(t, 1, f.store.tokenCount(res.User.ID, entity.TokenVerify), "a verification token is issued")
			assert.NotEmpty(t, res.RefreshToken)

			claims, err := f.signer.Verify(res.AccessToken)
			require.NoError(t, err)
			assert.Equal(t, res.User.ID, claims.Subject)
			assert.Equal(t, entity.RoleUser, claims.Role)
		})
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		login    string
		password string
		lock     *entity.User
		wantErr  error
	}{
		{name: "by email", login: "v1@example.com", password: "secret123"},
		{name: "by username, any case", login: "Viewer_1", password: "secret123"},
		{name: "wrong password", login: "viewer_1", password: "nope12345", wantErr: entity.ErrInvalidCredentials},
		{name: "unknown account", login: "ghost", password: "secret123", wantErr: entity.ErrInvalidCredentials},
		{name: "locked account", login: "viewer_1", password: "secret123", lock: &entity.User{Status: entity.UserLocked}, wantErr: entity.ErrAccountLocked},
		{name: "deleted account", login: "viewer_1", password: "secret123", lock: &entity.User{Status: entity.UserDeleted}, wantErr: entity.ErrInvalidCredentials},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)

			if tc.lock != nil {
				u := f.store.user(reg.User.ID)
				u.Status = tc.lock.Status
				f.store.setUser(u)
			}

			res, err := f.uc.Login(context.Background(), usecase.LoginInput{Login: tc.login, Password: tc.password, Client: _client})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, reg.User.ID, res.User.ID)
		})
	}
}

func TestRefresh_Rotates(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	reg := f.register(t)

	next, err := f.uc.Refresh(context.Background(), reg.RefreshToken, _client)
	require.NoError(t, err)
	assert.NotEqual(t, reg.RefreshToken, next.RefreshToken)
	assert.Equal(t, 1, f.sessions.activeFor(reg.User.ID))
}

func TestRefresh_ReuseRevokesEverySession(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	reg := f.register(t)

	_, err := f.uc.Login(context.Background(), usecase.LoginInput{Login: "viewer_1", Password: "secret123", Client: _client})
	require.NoError(t, err)

	_, err = f.uc.Refresh(context.Background(), reg.RefreshToken, _client)
	require.NoError(t, err)
	require.Equal(t, 2, f.sessions.activeFor(reg.User.ID))

	_, err = f.uc.Refresh(context.Background(), reg.RefreshToken, _client)
	require.ErrorIs(t, err, entity.ErrSessionExpired)
	assert.Equal(t, 0, f.sessions.activeFor(reg.User.ID))
}

func TestRefresh_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		token   func(reg usecase.AuthResult) string
		advance time.Duration
	}{
		{name: "empty token", token: func(usecase.AuthResult) string { return "" }},
		{name: "unknown token", token: func(usecase.AuthResult) string { return "unknown" }},
		{name: "expired session", token: func(r usecase.AuthResult) string { return r.RefreshToken }, advance: 25 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)
			*f.clock = f.clock.Add(tc.advance)

			_, err := f.uc.Refresh(context.Background(), tc.token(reg), _client)
			require.ErrorIs(t, err, entity.ErrSessionExpired)
		})
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	reg := f.register(t)

	require.NoError(t, f.uc.Logout(context.Background(), reg.RefreshToken))
	require.NoError(t, f.uc.Logout(context.Background(), "unknown"))

	_, err := f.uc.Refresh(context.Background(), reg.RefreshToken, _client)
	require.ErrorIs(t, err, entity.ErrSessionExpired)
}

func TestArgon2(t *testing.T) {
	t.Parallel()

	h := auth.NewArgon2(auth.Argon2Params{Memory: 64, Time: 1, Threads: 1})

	encoded, err := h.Hash("secret123")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$m=64,t=1,p=1$"))

	ok, err := h.Verify("secret123", encoded)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = h.Verify("secret124", encoded)
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = h.Verify("secret123", "$bcrypt$x")
	require.Error(t, err)
}
