package entity

import (
	"errors"
	"time"
)

// PlaybackTTL is how long an opened playback session stays valid (signed URLs reuse it from GĐ5).
const PlaybackTTL = 4 * time.Hour

// Resume rules shared with the web client (src/features/vod/lib/progress.ts).
const (
	// MinResumeSec: below this nothing worth resuming was watched.
	MinResumeSec = 5
	// EndMarginSec: this close to the end counts as finished.
	EndMarginSec = 10
	// positionSlackSec tolerates players reporting slightly past the stored duration.
	positionSlackSec = 5
)

// PlaybackTarget says what a session plays: exactly one of StreamID and VODID is set.
type PlaybackTarget struct {
	StreamID string
	VODID    string
}

// PlaybackSession is one viewer opening one stream or VOD (playback_sessions).
type PlaybackSession struct {
	ID                 string
	Target             PlaybackTarget
	UserID             string // empty for guests
	AnonID             string // set for guests
	MatureAcknowledged bool
	CreatedAt          time.Time
	ExpiresAt          time.Time
}

// MatureGate is the age-gate outcome for a mature stream or VOD.
type MatureGate struct {
	Required     bool
	Acknowledged bool
}

// Open reports whether the media may be handed out.
func (g MatureGate) Open() bool {
	return !g.Required || g.Acknowledged
}

// PlaybackGrant answers an open-playback request. ObjectKey and Session are empty while the gate is closed.
type PlaybackGrant struct {
	Session   *PlaybackSession
	ObjectKey string
	AnonID    string
	Mature    MatureGate
}

// WatchProgress is a signed-in viewer's position in a VOD (watch_progress).
type WatchProgress struct {
	UserID      string
	VODID       string
	PositionSec int
	UpdatedAt   time.Time
}

// ContinueItem is a VOD the viewer has not finished.
type ContinueItem struct {
	VOD         VOD
	PositionSec int
	UpdatedAt   time.Time
}

// ErrInvalidPosition: a watch position below zero or beyond the VOD's length.
var ErrInvalidPosition = errors.New("invalid watch position")

// ValidPosition reports whether pos fits a VOD of durationSec (0 = unknown length).
func ValidPosition(pos, durationSec int) bool {
	if pos < 0 {
		return false
	}

	return durationSec <= 0 || pos <= durationSec+positionSlackSec
}
