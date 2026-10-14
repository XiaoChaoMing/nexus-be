package entity

import (
	"slices"
	"time"
)

// Field detail codes (NXL-00 §4.1).
const (
	DetailRequired = "REQUIRED"
	DetailLength   = "LENGTH"
	DetailEnum     = "ENUM"
	DetailFormat   = "FORMAT"
)

// Values allowed by NXL-B API 04 (and the chat_settings CHECK constraints): BR09 slow mode seconds,
// BR10 followers-only minutes.
func slowValues() []int      { return []int{0, 2, 5, 10, 30, 60, 120} }
func followersValues() []int { return []int{0, 10, 30, 60, 1440, 10080, 43200, 129600} }

// ChatSettings are a channel's chat modes. FollowersMin nil = Followers-only off.
type ChatSettings struct {
	ChannelID    string
	SlowSec      int
	FollowersMin *int
	SubsOnly     bool
	UpdatedBy    *string
	// UpdatedAt is nil for a channel that never saved settings (defaults).
	UpdatedAt *time.Time
}

// Validate checks the enumerations of NXL-B API 04.
func (s ChatSettings) Validate() error {
	details := []string{}

	if !slices.Contains(slowValues(), s.SlowSec) {
		details = append(details, "slowSec", DetailEnum)
	}

	if s.FollowersMin != nil && !slices.Contains(followersValues(), *s.FollowersMin) {
		details = append(details, "followersMin", DetailEnum)
	}

	if len(details) > 0 {
		return Reject(ErrValidation, details...)
	}

	return nil
}

// SameModes reports whether two settings enforce the same modes.
func (s ChatSettings) SameModes(o ChatSettings) bool {
	sameFollowers := (s.FollowersMin == nil) == (o.FollowersMin == nil) &&
		(s.FollowersMin == nil || *s.FollowersMin == *o.FollowersMin)

	return s.SlowSec == o.SlowSec && s.SubsOnly == o.SubsOnly && sameFollowers
}

// Mode is the JSON form of the modes in snapshots and mode_change events.
type Mode struct {
	SlowSec      int  `json:"slowSec"`
	FollowersMin *int `json:"followersMin"`
	SubsOnly     bool `json:"subsOnly"`
}

// Mode -.
func (s ChatSettings) Mode() Mode {
	return Mode{SlowSec: s.SlowSec, FollowersMin: s.FollowersMin, SubsOnly: s.SubsOnly}
}
