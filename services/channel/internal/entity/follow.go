package entity

import "time"

// NotifyLevel is the follower's bell (BR-D19).
type NotifyLevel string

// Notification levels.
const (
	NotifyAll      NotifyLevel = "ALL"
	NotifyLiveOnly NotifyLevel = "LIVE_ONLY"
	NotifyOff      NotifyLevel = "OFF"
)

// Valid -.
func (l NotifyLevel) Valid() bool {
	return l == NotifyAll || l == NotifyLiveOnly || l == NotifyOff
}

// Follow -.
type Follow struct {
	UserID      string
	ChannelID   string
	NotifyLevel NotifyLevel
	CreatedAt   time.Time
}

// FollowingFilter selects the followed channels listed.
type FollowingFilter string

// Following filters.
const (
	FollowingAll     FollowingFilter = "all"
	FollowingLive    FollowingFilter = "live"
	FollowingOffline FollowingFilter = "offline"
)

// Valid -.
func (f FollowingFilter) Valid() bool {
	return f == FollowingAll || f == FollowingLive || f == FollowingOffline
}

// Sidebar and following list limits (NXL-C).
const (
	SidebarFollowedMax = 10
	SidebarLimitMax    = 50
	// FollowingLiveMax bounds the in-memory sort of followed live channels by viewers.
	FollowingLiveMax = 200
)

// FollowedChannel is one channel the user follows.
type FollowedChannel struct {
	Channel       Channel
	CurrentStream *LiveStream
	NotifyLevel   NotifyLevel
	FollowedAt    time.Time
}

// Viewers is the live viewer count used to sort (0 when offline or unknown).
func (f FollowedChannel) Viewers() int {
	if f.CurrentStream == nil || f.CurrentStream.ViewerCount == nil {
		return 0
	}

	return *f.CurrentStream.ViewerCount
}

// Sidebar is the "followed channels" block of the shell.
type Sidebar struct {
	Live           []FollowedChannel
	Offline        []FollowedChannel
	TotalFollowing int
}

// LiveStream is channel-svc's copy of a stream on air (live_streams).
type LiveStream struct {
	StreamID   string
	ChannelID  string
	Title      string
	CategoryID *string
	Category   *Category
	Tags       []string
	Language   string
	IsMature   bool
	StartedAt  time.Time
	// ViewerCount is nil when unknown (Redis not configured or down).
	ViewerCount *int
}
