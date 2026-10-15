// Package entity holds media-svc domain types.
package entity

import "errors"

// Rendition is one output quality of the ABR ladder.
type Rendition struct {
	Name        string // 720p, 480p, 360p; also the HLS sub-directory
	Height      int
	VideoKbps   int
	MaxrateKbps int
	BufsizeKbps int
	AudioKbps   int
}

// Source describes the incoming publish as announced by onMetaData.
type Source struct {
	Width    int
	Height   int
	HasAudio bool
}

var (
	// ErrUnauthorized -.
	ErrUnauthorized = errors.New("stream key rejected")
	// ErrAlreadyLive -.
	ErrAlreadyLive = errors.New("channel already live")
	// ErrCapacity -.
	ErrCapacity = errors.New("too many concurrent streams")
	// ErrRecentlyEnded -.
	ErrRecentlyEnded = errors.New("channel ended a stream moments ago")
	// ErrSuspended: an admin suspended the channel.
	ErrSuspended = errors.New("channel suspended")
	// ErrUnsupportedCodec -.
	ErrUnsupportedCodec = errors.New("unsupported codec: H.264 video and AAC audio required")
)
