package entity

import "time"

// TokenPurpose mirrors email_tokens.purpose.
type TokenPurpose string

// E-mail token purposes.
const (
	TokenVerify TokenPurpose = "VERIFY"
	TokenReset  TokenPurpose = "RESET"
)

// TokenStats summarizes the recent e-mail tokens of one user and purpose (rate limiting).
type TokenStats struct {
	// LastIssuedAt is nil when no token was ever issued.
	LastIssuedAt *time.Time
	// OldestInWindow is the oldest token created inside the window, nil when there is none.
	OldestInWindow *time.Time
	// IssuedInWindow counts tokens created since the start of the window.
	IssuedInWindow int
}

// TokenLimit is the issuing rate limit of e-mail tokens.
type TokenLimit struct {
	MinInterval time.Duration
	MaxInWindow int
	Window      time.Duration
}

// RetryAfter returns how long to wait before another token may be issued (0 = now).
func (l TokenLimit) RetryAfter(s TokenStats, now time.Time) time.Duration {
	var wait time.Duration

	if s.LastIssuedAt != nil {
		wait = max(wait, s.LastIssuedAt.Add(l.MinInterval).Sub(now))
	}

	if s.IssuedInWindow >= l.MaxInWindow && s.OldestInWindow != nil {
		wait = max(wait, s.OldestInWindow.Add(l.Window).Sub(now))
	}

	return wait
}
