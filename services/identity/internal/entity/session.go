package entity

import "time"

// Session is a refresh-token session in nxl_identity.sessions.
type Session struct {
	ID               string
	UserID           string
	RefreshTokenHash string
	UserAgent        string
	IP               string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
}

// IsRevoked -.
func (s Session) IsRevoked() bool {
	return s.RevokedAt != nil
}

// IsExpired -.
func (s Session) IsExpired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}
