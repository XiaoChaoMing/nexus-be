package entity

import "time"

// Range is the period of the Studio dashboard.
type Range string

// Dashboard ranges.
const (
	Range7d  Range = "7d"
	Range28d Range = "28d"
)

const _day = 24 * time.Hour

// Duration returns the range length; ok is false for an unknown range.
func (r Range) Duration() (time.Duration, bool) {
	switch r {
	case Range7d:
		return 7 * _day, true //nolint:mnd // a week
	case Range28d:
		return 28 * _day, true //nolint:mnd // four weeks
	}

	return 0, false
}

// RecentStreamsMax is how many ended streams the dashboard lists.
const RecentStreamsMax = 5

// RecentStream is an ended broadcast on the dashboard.
type RecentStream struct {
	StreamID      string
	Title         string
	StartedAt     *time.Time
	EndedAt       *time.Time
	EndReason     *string
	PeakViewers   int
	VODID         *string
	VODVisibility *string
}

// DashboardStats are counted on the tables directly (BR-G2-03).
type DashboardStats struct {
	StreamCount    int
	LiveSeconds    int64
	VODReadyPublic int
	VODReadyHidden int
}

// Dashboard is stream-svc's half of the Studio overview.
type Dashboard struct {
	Range   Range
	Current *Stream
	Recent  []RecentStream
	Stats   DashboardStats
}
