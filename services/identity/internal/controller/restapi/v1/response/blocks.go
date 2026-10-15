package response

import (
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

// UserRef is a user shown in a list. AvatarURL is always null: accounts have no picture yet.
type UserRef struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// Block is one blocked account.
type Block struct {
	User      UserRef   `json:"user"`
	CreatedAt time.Time `json:"createdAt"`
}

// BlockPage is newest first; nextCursor is null on the last page.
type BlockPage struct {
	Items      []Block `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// BlockedID -.
type BlockedID struct {
	UserID string `json:"userId"`
}

// BlockedIDs is GET /me/blocks?fields=ids: every blocked id in one page.
type BlockedIDs struct {
	Items      []BlockedID `json:"items"`
	NextCursor *string     `json:"nextCursor"`
}

// NewBlockPage -.
func NewBlockPage(p usecase.BlockPage) BlockPage {
	out := BlockPage{Items: make([]Block, 0, len(p.Items))}

	for _, b := range p.Items {
		out.Items = append(out.Items, Block{
			User:      UserRef{ID: b.UserID, Username: b.Username, DisplayName: displayName(b.DisplayName, b.Username)},
			CreatedAt: b.CreatedAt.UTC(),
		})
	}

	if p.NextCursor != "" {
		out.NextCursor = &p.NextCursor
	}

	return out
}

// NewBlockedIDs -.
func NewBlockedIDs(ids []string) BlockedIDs {
	out := BlockedIDs{Items: make([]BlockedID, 0, len(ids))}
	for _, id := range ids {
		out.Items = append(out.Items, BlockedID{UserID: id})
	}

	return out
}

func displayName(name, username string) string {
	if name != "" {
		return name
	}

	return username
}

// AdminUser is GET /admin/users/{id}. An expired lock reads as ACTIVE.
type AdminUser struct {
	ID            string     `json:"id"`
	Username      string     `json:"username"`
	DisplayName   string     `json:"displayName"`
	Email         string     `json:"email"`
	EmailVerified bool       `json:"emailVerified"`
	GlobalRole    string     `json:"globalRole"  example:"USER"`
	Status        string     `json:"status"      example:"ACTIVE"`
	LockedUntil   *time.Time `json:"lockedUntil"`
	// LockKind is FAILED_LOGIN or ADMIN while locked.
	LockKind   *string   `json:"lockKind"`
	LockReason *string   `json:"lockReason"`
	ChannelID  *string   `json:"channelId"`
	CreatedAt  time.Time `json:"createdAt"`
}

// NewAdminUser -.
func NewAdminUser(u entity.User) AdminUser {
	out := AdminUser{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email, EmailVerified: u.IsEmailVerified(),
		GlobalRole: u.GlobalRole, Status: string(u.Status), LockKind: u.LockKind, LockReason: u.LockReason,
		ChannelID: u.ChannelID, CreatedAt: u.CreatedAt.UTC(),
	}

	if u.LockedUntil != nil {
		t := u.LockedUntil.UTC()
		out.LockedUntil = &t
	}

	return out
}
