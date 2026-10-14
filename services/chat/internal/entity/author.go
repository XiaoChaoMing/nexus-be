package entity

import (
	"hash/fnv"
	"strconv"
)

// usernameColors are 12 colors with a contrast of at least 4.5:1 on the dark chat surface (NXL-B BR16).
func usernameColors() []string {
	return []string{
		"#FF8A8A", "#FFA94D", "#FFD43B", "#A9E34B", "#69DB7C", "#63E6BE",
		"#66D9E8", "#74C0FC", "#91A7FF", "#B197FC", "#E599F7", "#FAA2C1",
	}
}

// ColorOf picks the user's color deterministically from the user id.
func ColorOf(userID string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID)) // hash.Hash never returns an error

	colors := usernameColors()

	return colors[h.Sum32()%uint32(len(colors))] //nolint:gosec // 12 colors, no overflow
}

// Standing is what a user is in a channel; it decides badges and exemptions.
type Standing struct {
	Streamer  bool
	Moderator bool
	VIP       bool
	Admin     bool
	// SubTier is 0 when not subscribed.
	SubTier int
	// FollowedMinutes is nil when the user does not follow the channel.
	FollowedMinutes *int
}

// Badges returns at most MaxBadges badges, highest first (BR15). Admins have no badge of their own.
func (s Standing) Badges() []string {
	out := make([]string, 0, MaxBadges)

	add := func(ok bool, b string) {
		if ok && len(out) < MaxBadges {
			out = append(out, b)
		}
	}

	add(s.Streamer, BadgeStreamer)
	add(s.Moderator, BadgeModerator)
	add(s.VIP, BadgeVIP)
	add(s.SubTier > 0, BadgeSubPrefix+strconv.Itoa(s.SubTier))

	return out
}

// Privileged: Streamer, Moderator and Admin skip every chat mode and get the higher rate ceiling (BR14).
func (s Standing) Privileged() bool { return s.Streamer || s.Moderator || s.Admin }

// SkipsSlow: privileged roles and VIPs (NXL-B Q3).
func (s Standing) SkipsSlow() bool { return s.Privileged() || s.VIP }

// SkipsSubsOnly: privileged roles and subscribers.
func (s Standing) SkipsSubsOnly() bool { return s.Privileged() || s.SubTier > 0 }

// Role is the caller's role in snapshots (me.role).
func (s Standing) Role() string {
	switch {
	case s.Streamer:
		return "STREAMER"
	case s.Moderator:
		return "MODERATOR"
	case s.Admin:
		return GlobalRoleAdmin
	case s.VIP:
		return "VIP"
	case s.SubTier > 0:
		return "SUBSCRIBER"
	default:
		return "VIEWER"
	}
}
