package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	_password    = "secret123"
	_newPassword = "newsecret456"
	_maxFailures = 10 // auth.Config default
)

func (f *fixture) login(password string) (usecase.AuthResult, error) {
	return f.uc.Login(context.Background(), usecase.LoginInput{Login: "viewer_1", Password: password, Client: _client})
}

func (f *fixture) advance(d time.Duration) {
	*f.clock = f.clock.Add(d)
}

func TestLogin_Lockout(t *testing.T) { //nolint:funlen // one table, several timelines
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, f *fixture, userID string)
	}{
		{name: "locks at the limit and reports until when", run: func(t *testing.T, f *fixture, userID string) {
			t.Helper()

			for range _maxFailures - 1 {
				_, err := f.login("wrong1234")
				require.ErrorIs(t, err, entity.ErrInvalidCredentials)
			}

			_, err := f.login("wrong1234")

			var locked *entity.LockedError
			require.ErrorAs(t, err, &locked)
			require.ErrorIs(t, err, entity.ErrAccountLocked)
			assert.Equal(t, f.clock.Add(15*time.Minute), locked.Until)
			assert.Equal(t, entity.UserLocked, f.store.user(userID).Status)
		}},
		{name: "temporary lock refuses even the right password, without counting", run: func(t *testing.T, f *fixture, userID string) {
			t.Helper()
			lockOut(t, f)
			f.advance(time.Minute)

			_, err := f.login(_password)
			require.ErrorIs(t, err, entity.ErrAccountLocked)

			_, err = f.login("wrong1234")
			require.ErrorIs(t, err, entity.ErrAccountLocked)
			assert.Equal(t, _maxFailures, f.store.user(userID).FailedLogins)
		}},
		{name: "expired lock: right password signs in and reactivates", run: func(t *testing.T, f *fixture, userID string) {
			t.Helper()
			lockOut(t, f)
			f.advance(16 * time.Minute)

			_, err := f.login(_password)
			require.NoError(t, err)

			u := f.store.user(userID)
			assert.Equal(t, entity.UserActive, u.Status)
			assert.Nil(t, u.LockedUntil)
			assert.Zero(t, u.FailedLogins)
		}},
		{name: "success resets the counter", run: func(t *testing.T, f *fixture, userID string) {
			t.Helper()

			for range _maxFailures - 1 {
				_, err := f.login("wrong1234")
				require.ErrorIs(t, err, entity.ErrInvalidCredentials)
			}

			_, err := f.login(_password)
			require.NoError(t, err)
			assert.Zero(t, f.store.user(userID).FailedLogins)

			_, err = f.login("wrong1234")
			require.ErrorIs(t, err, entity.ErrInvalidCredentials, "counting restarts at 1")
		}},
		{name: "failures outside the window do not add up", run: func(t *testing.T, f *fixture, userID string) {
			t.Helper()

			for range _maxFailures - 1 {
				_, err := f.login("wrong1234")
				require.ErrorIs(t, err, entity.ErrInvalidCredentials)
			}

			f.advance(16 * time.Minute)

			_, err := f.login("wrong1234")
			require.ErrorIs(t, err, entity.ErrInvalidCredentials)
			assert.Equal(t, 1, f.store.user(userID).FailedLogins)
		}},
		{name: "admin lock stays hidden behind a wrong password", run: func(t *testing.T, f *fixture, userID string) {
			t.Helper()

			u := f.store.user(userID)
			u.Status = entity.UserLocked
			f.store.setUser(u)

			_, err := f.login("wrong1234")
			require.ErrorIs(t, err, entity.ErrInvalidCredentials)
			assert.Zero(t, f.store.user(userID).FailedLogins, "no counting on admin-locked accounts")

			_, err = f.login(_password)
			require.ErrorIs(t, err, entity.ErrAccountLocked)

			var locked *entity.LockedError
			assert.False(t, errors.As(err, &locked), "no lockedUntil for a permanent lock")
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)
			tc.run(t, f, reg.User.ID)
		})
	}
}

func lockOut(t *testing.T, f *fixture) {
	t.Helper()

	for range _maxFailures {
		_, err := f.login("wrong1234")
		require.Error(t, err)
	}

	_, err := f.login(_password)
	require.ErrorIs(t, err, entity.ErrAccountLocked)
}

func TestVerifyEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		token   func(f *fixture, userID string) string
		advance time.Duration
		wantErr error
	}{
		{name: "valid token", token: verifyToken},
		{name: "empty token", token: func(*fixture, string) string { return "" }, wantErr: entity.ErrTokenInvalid},
		{name: "unknown token", token: func(*fixture, string) string { return "nope" }, wantErr: entity.ErrTokenInvalid},
		{name: "expired token", token: verifyToken, advance: 25 * time.Hour, wantErr: entity.ErrTokenInvalid},
		{name: "superseded by a resend", token: func(f *fixture, userID string) string {
			old := verifyToken(f, userID)
			f.advance(2 * time.Minute)

			if err := f.uc.ResendVerification(context.Background(), userID); err != nil {
				return "resend failed: " + err.Error()
			}

			return old
		}, wantErr: entity.ErrTokenInvalid},
		{name: "reset token is not a verify token", token: resetToken, wantErr: entity.ErrTokenInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)
			token := tc.token(f, reg.User.ID)
			f.advance(tc.advance)

			user, err := f.uc.VerifyEmail(context.Background(), token)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.True(t, user.IsEmailVerified())
			assert.Contains(t, f.store.events(), "user.email_verified.v1")

			_, err = f.uc.VerifyEmail(context.Background(), token)
			require.ErrorIs(t, err, entity.ErrTokenInvalid, "one-time")
		})
	}
}

func verifyToken(f *fixture, userID string) string {
	return f.store.lastToken(userID, entity.TokenVerify)
}

func TestResendVerification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(t *testing.T, f *fixture, userID string)
		wantErr   error
		wantRetry time.Duration
	}{
		{name: "right after register", wantErr: entity.ErrRateLimited, wantRetry: 60 * time.Second},
		{name: "after the cooldown", prepare: func(_ *testing.T, f *fixture, _ string) { f.advance(61 * time.Second) }},
		{name: "five per hour", prepare: func(t *testing.T, f *fixture, userID string) {
			t.Helper()

			for range 4 {
				f.advance(61 * time.Second)
				require.NoError(t, f.uc.ResendVerification(context.Background(), userID))
			}

			f.advance(61 * time.Second)
		}, wantErr: entity.ErrRateLimited, wantRetry: time.Hour - 5*61*time.Second},
		{name: "already verified", prepare: func(t *testing.T, f *fixture, userID string) {
			t.Helper()

			_, err := f.uc.VerifyEmail(context.Background(), verifyToken(f, userID))
			require.NoError(t, err)
		}, wantErr: entity.ErrAlreadyVerified},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)

			if tc.prepare != nil {
				tc.prepare(t, f, reg.User.ID)
			}

			before := f.store.tokenCount(reg.User.ID, entity.TokenVerify)
			err := f.uc.ResendVerification(context.Background(), reg.User.ID)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Equal(t, before, f.store.tokenCount(reg.User.ID, entity.TokenVerify))

				var retry *entity.RetryError
				if tc.wantRetry > 0 {
					require.ErrorAs(t, err, &retry)
					assert.Equal(t, tc.wantRetry, retry.RetryAfter)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, before+1, f.store.tokenCount(reg.User.ID, entity.TokenVerify))
		})
	}
}

func TestForgotPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		email      string
		repeat     int
		wantTokens int
	}{
		{name: "known e-mail, any case", email: "V1@example.com", wantTokens: 1},
		{name: "unknown e-mail", email: "ghost@example.com"},
		{name: "rate limited silently", email: "v1@example.com", repeat: 3, wantTokens: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)

			for range tc.repeat + 1 {
				require.NoError(t, f.uc.ForgotPassword(context.Background(), tc.email))
			}

			assert.Equal(t, tc.wantTokens, f.store.tokenCount(reg.User.ID, entity.TokenReset))
		})
	}
}

func TestResetPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		token   func(f *fixture, userID string) string
		advance time.Duration
		wantErr error
	}{
		{name: "valid token", token: resetToken},
		{name: "expired after 30 minutes", token: resetToken, advance: 31 * time.Minute, wantErr: entity.ErrTokenInvalid},
		{name: "verify token is not a reset token", token: verifyToken, wantErr: entity.ErrTokenInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)
			lockOut(t, f)

			token := tc.token(f, reg.User.ID)
			f.advance(tc.advance)

			err := f.uc.ResetPassword(context.Background(), token, _newPassword)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Zero(t, f.sessions.activeFor(reg.User.ID), "every session is revoked")
			assert.Equal(t, entity.UserActive, f.store.user(reg.User.ID).Status, "temporary lock lifted")

			_, err = f.login(_newPassword)
			require.NoError(t, err)

			require.ErrorIs(t, f.uc.ResetPassword(context.Background(), token, _newPassword), entity.ErrTokenInvalid, "one-time")
		})
	}
}

func resetToken(f *fixture, userID string) string {
	if err := f.uc.ForgotPassword(context.Background(), "v1@example.com"); err != nil {
		return ""
	}

	return f.store.lastToken(userID, entity.TokenReset)
}

func TestChangePassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		wantErr error
	}{
		{name: "right current password", current: _password},
		{name: "wrong current password", current: "wrong1234", wantErr: entity.ErrWrongPassword},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)

			other, err := f.login(_password)
			require.NoError(t, err)

			res, err := f.uc.ChangePassword(context.Background(), usecase.ChangePasswordInput{
				UserID: reg.User.ID, CurrentPassword: tc.current, NewPassword: _newPassword, Client: _client,
			})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Equal(t, 2, f.sessions.activeFor(reg.User.ID))

				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, res.AccessToken)
			assert.Equal(t, 1, f.sessions.activeFor(reg.User.ID), "only the new session survives")

			_, err = f.uc.Refresh(context.Background(), other.RefreshToken, _client)
			require.ErrorIs(t, err, entity.ErrSessionExpired)

			_, err = f.uc.Refresh(context.Background(), res.RefreshToken, _client)
			require.NoError(t, err)

			_, err = f.login(_newPassword)
			require.NoError(t, err)
		})
	}
}

func TestUpdateProfile(t *testing.T) { //nolint:funlen // table
	t.Parallel()

	ptr := func(s string) *string { return &s }
	yes := true

	tests := []struct {
		name    string
		first   *usecase.ProfileUpdate
		in      usecase.ProfileUpdate
		wantErr error
		check   func(t *testing.T, p usecase.Profile, events []string)
	}{
		{
			name: "display name trimmed and announced", in: usecase.ProfileUpdate{DisplayName: ptr("  Minh  ")},
			check: func(t *testing.T, p usecase.Profile, events []string) {
				t.Helper()
				assert.Equal(t, "Minh", p.User.DisplayName)
				assert.Contains(t, events, "user.profile_updated.v1")
			},
		},
		{
			name: "unchanged display name is not announced", in: usecase.ProfileUpdate{DisplayName: ptr("viewer_1")},
			check: func(t *testing.T, _ usecase.Profile, events []string) {
				t.Helper()
				assert.NotContains(t, events, "user.profile_updated.v1")
			},
		},
		{
			name: "birth date set once", in: usecase.ProfileUpdate{BirthDate: ptr("2000-01-31")},
			check: func(t *testing.T, p usecase.Profile, _ []string) {
				t.Helper()
				require.NotNil(t, p.User.BirthDate)
				assert.Equal(t, "2000-01-31", p.User.BirthDate.Format(entity.DateLayout))
			},
		},
		{
			name: "birth date already set", first: &usecase.ProfileUpdate{BirthDate: ptr("2000-01-31")},
			in: usecase.ProfileUpdate{BirthDate: ptr("2001-01-31")}, wantErr: entity.ErrBirthDateAlreadySet,
		},
		{name: "birth date in the future", in: usecase.ProfileUpdate{BirthDate: ptr("2999-01-01")}, wantErr: entity.ErrInvalidBirthDate},
		{name: "preferences merge", in: usecase.ProfileUpdate{
			Preferences: &usecase.PreferencesUpdate{MatureVisible: &yes},
			Locale:      ptr("en"), Timezone: ptr("Europe/Paris"),
		}, check: func(t *testing.T, p usecase.Profile, _ []string) {
			t.Helper()
			assert.True(t, p.User.Preferences.MatureVisible)
			assert.False(t, p.User.Preferences.SidebarCollapsed)
			assert.Equal(t, "en", p.User.Locale)
			assert.Equal(t, "Europe/Paris", p.User.Timezone)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			reg := f.register(t)
			ctx := context.Background()

			if tc.first != nil {
				_, err := f.uc.UpdateProfile(ctx, reg.User.ID, *tc.first)
				require.NoError(t, err)
			}

			p, err := f.uc.UpdateProfile(ctx, reg.User.ID, tc.in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			tc.check(t, p, f.store.events())

			got, err := f.uc.Profile(ctx, reg.User.ID)
			require.NoError(t, err)
			assert.Equal(t, p.User, got.User)
		})
	}
}
