// Package entity holds channel-svc domain types (NXL-D §4, BR-D14, BR-D15).
package entity

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Status of a channel.
type Status string

// Statuses.
const (
	StatusActive    Status = "ACTIVE"
	StatusSuspended Status = "SUSPENDED"
)

// Handle and display name limits (BR-D14, channels.display_name).
const (
	HandleMinLen      = 3
	HandleMaxLen      = 25
	DisplayNameMaxLen = 50

	_handlePad      = "_"
	_handleFallback = "channel"
)

// Channel is a creator's public page.
type Channel struct {
	ID              string
	OwnerID         string
	Handle          string
	DisplayName     string
	Bio             *string
	AvatarURL       *string
	BannerURL       *string
	SocialLinks     []SocialLink
	Verified        bool
	MatureDefault   bool
	Status          Status
	IsLive          bool
	CurrentStreamID *string
	FollowerCount   int
	LastLiveAt      *time.Time
	HandleChangedAt *time.Time
	CreatedAt       time.Time
}

// Suspended reports whether the channel is hidden from viewers.
func (c Channel) Suspended() bool {
	return c.Status == StatusSuspended
}

// _handleRe matches what channels.handle accepts; lookups are case-insensitive (citext).
var _handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,25}$`)

// ValidHandle reports whether s can be a handle at all (anything else cannot exist).
func ValidHandle(s string) bool {
	return _handleRe.MatchString(s)
}

// BaseHandle derives the preferred handle from a username: lower case, only [a-z0-9_],
// padded with "_" to 3 characters and cut to 25.
func BaseHandle(username string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(username) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}

	h := b.String()
	if h == "" {
		h = _handleFallback
	}

	if len(h) < HandleMinLen {
		h += strings.Repeat(_handlePad, HandleMinLen-len(h))
	}

	if len(h) > HandleMaxLen {
		h = h[:HandleMaxLen]
	}

	return h
}

// HandleCandidate returns the n-th handle to try for base (E-D13): n = 1 is base itself, then
// base2, base3 … with base cut so that the result stays within 25 characters.
func HandleCandidate(base string, n int) string {
	if n <= 1 {
		return base
	}

	suffix := strconv.Itoa(n)
	if keep := HandleMaxLen - len(suffix); len(base) > keep {
		base = base[:keep]
	}

	return base + suffix
}

// DisplayName returns the trimmed display name, falling back to the username, cut to 50 characters.
func DisplayName(displayName, username string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = strings.TrimSpace(username)
	}

	if utf8.RuneCountInString(name) > DisplayNameMaxLen {
		name = string([]rune(name)[:DisplayNameMaxLen])
	}

	return name
}
