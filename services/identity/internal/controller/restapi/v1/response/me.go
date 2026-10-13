package response

import (
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

// Preferences -.
type Preferences struct {
	SidebarCollapsed bool `json:"sidebarCollapsed"`
	MatureVisible    bool `json:"matureVisible"`
}

// Me is GET/PATCH /v1/me.
type Me struct {
	User User `json:"user"`
	// Always VIEWER; CREATOR when the user owns a channel; ADMIN for admins.
	Roles               []string    `json:"roles"     example:"VIEWER"`
	IsCreator           bool        `json:"isCreator"`
	ChannelID           *string     `json:"channelId"`
	UnreadNotifications int         `json:"unreadNotifications"`
	Preferences         Preferences `json:"preferences"`
}

// NewMe -.
func NewMe(p usecase.Profile) Me {
	u := p.User

	return Me{
		User:                NewUser(u),
		Roles:               u.Roles(),
		IsCreator:           u.IsCreator(),
		ChannelID:           u.ChannelID,
		UnreadNotifications: p.UnreadNotifications,
		Preferences:         newPreferences(u.Preferences),
	}
}

func newPreferences(p entity.Preferences) Preferences {
	return Preferences{SidebarCollapsed: p.SidebarCollapsed, MatureVisible: p.MatureVisible}
}
