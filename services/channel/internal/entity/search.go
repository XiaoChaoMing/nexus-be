package entity

import (
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// DocType is the kind of a search result.
type DocType string

// Search document kinds, in the order groups are shown (Channel → Live → Category → Video).
const (
	DocChannel  DocType = "channel"
	DocStream   DocType = "stream"
	DocCategory DocType = "category"
	DocVOD      DocType = "vod"
)

// DocTypes lists every kind in display order.
func DocTypes() []DocType {
	return []DocType{DocChannel, DocStream, DocCategory, DocVOD}
}

// Valid -.
func (t DocType) Valid() bool {
	switch t {
	case DocChannel, DocStream, DocCategory, DocVOD:
		return true
	}

	return false
}

// Search limits (NXL-00 §12).
const (
	SearchMinLength  = 2
	SearchMaxLength  = 100
	SearchGroupMax   = 5
	SearchLimitMax   = 10
	SearchHistoryMax = 10
)

// NormalizeQuery trims and NFC-normalizes q; ok is false when its length is outside 2–100 runes.
func NormalizeQuery(q string) (string, bool) {
	q = norm.NFC.String(strings.TrimSpace(q))
	n := utf8.RuneCountInString(q)

	return q, n >= SearchMinLength && n <= SearchMaxLength
}

// SearchDoc is one search hit.
type SearchDoc struct {
	Type     DocType
	ID       string
	Title    string
	Subtitle *string
	ImageURL *string
	Viewers  int
	IsLive   bool
	IsMature bool
	Target   string
	// Ranking keys, also the keyset cursor of the results page.
	PrefixHit bool
	Rank      int
}

// SearchGroup is the hits of one kind.
type SearchGroup struct {
	Type  DocType
	Items []SearchDoc
}

// SearchQuery is a remembered search of a signed-in user.
type SearchQuery struct {
	ID        string
	Query     string
	CreatedAt time.Time
}

// VODDoc is what the search index keeps of a VOD (stream.vod_ready, vod.updated).
type VODDoc struct {
	VODID        string
	StreamID     string
	ChannelID    string
	Title        string
	ThumbnailURL string
	CategoryID   *string
	IsMature     bool
	ExpiresAt    time.Time
	// Public is false when the VOD is hidden or not READY: the document is removed.
	Public bool
}

// StatsRange -.
type StatsRange string

// Studio stats ranges.
const (
	Range7d  StatsRange = "7d"
	Range28d StatsRange = "28d"
)

const _day = 24 * time.Hour

// Duration returns the range length; ok is false for an unknown range.
func (r StatsRange) Duration() (time.Duration, bool) {
	switch r {
	case Range7d:
		return 7 * _day, true //nolint:mnd // a week
	case Range28d:
		return 28 * _day, true //nolint:mnd // four weeks
	}

	return 0, false
}

// UpcomingOccurrence is the channel's next occurrence with its reminder count.
type UpcomingOccurrence struct {
	ScheduleID    string
	OccurrenceAt  time.Time
	Title         string
	ReminderCount int
}

// ChannelStats is channel-svc's half of the Studio dashboard.
type ChannelStats struct {
	Range         StatsRange
	FollowerCount int
	NewFollowers  int
	Upcoming      *UpcomingOccurrence
}
