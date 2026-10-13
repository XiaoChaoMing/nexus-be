package response

import (
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
)

// Data wraps every successful body.
type Data[T any] struct {
	Data T `json:"data"`
}

// Error wraps every error body.
type Error struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody -.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// User is the public projection of entity.User; it never carries credentials.
type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	DisplayName   string `json:"displayName"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"emailVerified"`
	GlobalRole    string `json:"globalRole"`
	// YYYY-MM-DD, null until set.
	BirthDate *string `json:"birthDate" example:"2000-01-31"`
	Locale    string  `json:"locale"    example:"vi"`
	Timezone  string  `json:"timezone"  example:"Asia/Ho_Chi_Minh"`
	IsCreator bool    `json:"isCreator"`
	// Null until the user owns a channel.
	ChannelID *string   `json:"channelId"`
	CreatedAt time.Time `json:"createdAt"`
}

// Session is returned by register, login, refresh and password change. The refresh token travels only in
// the cookie.
type Session struct {
	AccessToken string `json:"accessToken"`
	ExpiresIn   int    `json:"expiresIn"`
	User        User   `json:"user"`
}

// NewUser -.
func NewUser(u entity.User) User {
	return User{
		ID:            u.ID,
		Username:      u.Username,
		DisplayName:   u.DisplayName,
		Email:         u.Email,
		EmailVerified: u.IsEmailVerified(),
		GlobalRole:    u.GlobalRole,
		BirthDate:     date(u.BirthDate),
		Locale:        u.Locale,
		Timezone:      u.Timezone,
		IsCreator:     u.IsCreator(),
		ChannelID:     u.ChannelID,
		CreatedAt:     u.CreatedAt.UTC(),
	}
}

func date(t *time.Time) *string {
	if t == nil {
		return nil
	}

	s := t.Format(entity.DateLayout)

	return &s
}
