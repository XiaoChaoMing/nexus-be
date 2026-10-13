// Package usecase implements application business logic. Each logic group in own file.
package usecase

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
)

type (
	// ClientInfo describes where a sign-in comes from; stored on the session.
	ClientInfo struct {
		UserAgent string
		IP        string
	}

	// RegisterInput -.
	RegisterInput struct {
		Username    string
		DisplayName string
		Email       string
		Password    string
		Client      ClientInfo
	}

	// LoginInput -.
	LoginInput struct {
		// Login is an email or a username.
		Login    string
		Password string
		Client   ClientInfo
	}

	// ChangePasswordInput -.
	ChangePasswordInput struct {
		UserID          string
		CurrentPassword string
		NewPassword     string
		Client          ClientInfo
	}

	// AuthResult is returned by every call that signs the user in.
	AuthResult struct {
		User             entity.User
		AccessToken      string
		AccessExpiresIn  time.Duration
		RefreshToken     string
		RefreshExpiresAt time.Time
	}

	// PreferencesUpdate -.
	PreferencesUpdate struct {
		SidebarCollapsed *bool
		MatureVisible    *bool
	}

	// ProfileUpdate holds the PATCH /v1/me fields; nil means unchanged. BirthDate is "YYYY-MM-DD".
	ProfileUpdate struct {
		DisplayName *string
		BirthDate   *string
		Locale      *string
		Timezone    *string
		Preferences *PreferencesUpdate
	}

	// Profile is the signed-in user's account view (/v1/me).
	Profile struct {
		User                entity.User
		UnreadNotifications int
	}

	// Auth -.
	Auth interface {
		Register(ctx context.Context, in RegisterInput) (AuthResult, error)
		Login(ctx context.Context, in LoginInput) (AuthResult, error)
		Refresh(ctx context.Context, refreshToken string, client ClientInfo) (AuthResult, error)
		Logout(ctx context.Context, refreshToken string) error
		Me(ctx context.Context, userID string) (entity.User, error)
		// VerifyEmail consumes a VERIFY token; entity.ErrTokenInvalid when it cannot be used.
		VerifyEmail(ctx context.Context, token string) (entity.User, error)
		// ResendVerification issues a new VERIFY token; *entity.RetryError when asked too often.
		ResendVerification(ctx context.Context, userID string) error
		// ForgotPassword issues a RESET token when the account exists; it never reveals whether it does.
		ForgotPassword(ctx context.Context, email string) error
		// ResetPassword consumes a RESET token, stores the password and revokes every session.
		ResetPassword(ctx context.Context, token, password string) error
		// ChangePassword checks the current password, revokes every session and starts a new one.
		ChangePassword(ctx context.Context, in ChangePasswordInput) (AuthResult, error)
		Profile(ctx context.Context, userID string) (Profile, error)
		UpdateProfile(ctx context.Context, userID string, in ProfileUpdate) (Profile, error)
	}

	// SubmitInput is a creator application; BirthDate is "YYYY-MM-DD".
	SubmitInput struct {
		UserID    string
		LegalName string
		BirthDate string
		Note      string
	}

	// ApplicationPage is one page of the admin list; NextCursor is empty on the last page.
	ApplicationPage struct {
		Items      []entity.Application
		NextCursor string
	}

	// Creator handles creator applications (viewer and admin sides).
	Creator interface {
		Submit(ctx context.Context, in SubmitInput) (entity.Application, error)
		// Mine returns the user's newest application, nil when there is none.
		Mine(ctx context.Context, userID string) (*entity.Application, error)
		Cancel(ctx context.Context, userID, id string) error
		// List: status is an entity.ApplicationStatus or ALL; cursor is opaque.
		List(ctx context.Context, status, cursor string, limit int) (ApplicationPage, error)
		Get(ctx context.Context, id string) (entity.Application, error)
		Approve(ctx context.Context, adminID, id string) (entity.Application, error)
		Reject(ctx context.Context, adminID, id, reason string) (entity.Application, error)
	}

	// ChannelSync keeps identity's copy of channels (channel.created.v1).
	ChannelSync interface {
		ChannelCreated(ctx context.Context, eventID string, ch entity.UserChannel) error
	}

	// MailRequest is one auth.email_requested.v1 to deliver.
	MailRequest struct {
		UserID   string
		Email    string
		Username string
		Purpose  entity.TokenPurpose
		Token    string
		Locale   string
	}

	// Mail delivers account e-mails (identity-mailer consumer).
	Mail interface {
		Deliver(ctx context.Context, m MailRequest) error
	}
)
