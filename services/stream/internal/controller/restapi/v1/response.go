package v1

import (
	"time"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
)

// Data wraps every successful body.
type Data[T any] struct {
	Data T `json:"data"`
}

// ErrorResponse wraps every error body.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody -.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// StreamKeyCreated is the only response that ever carries the key.
type StreamKeyCreated struct {
	StreamKey string `json:"streamKey"`
	RTMPURL   string `json:"rtmpUrl"`
}

// StreamKeyInfo -.
type StreamKeyInfo struct {
	HasKey    bool       `json:"hasKey"`
	RTMPURL   string     `json:"rtmpUrl"`
	CreatedAt *time.Time `json:"createdAt"`
}

// Settings -.
type Settings struct {
	Title    string   `json:"title"`
	Tags     []string `json:"tags"`
	Language string   `json:"language"`
	IsMature bool     `json:"isMature"`
	// CategoryID is a channel-svc category (GET /v1/categories); null = none.
	CategoryID *string    `json:"categoryId"`
	UpdatedAt  *time.Time `json:"updatedAt"`
}

// CategoryRef is the category of a stream or VOD; the web client gets its slug and name from /v1/categories.
type CategoryRef struct {
	ID string `json:"id"`
}

// Channel is the summary of a creator's channel shown next to streams and VODs.
type Channel struct {
	ID          string  `json:"id"`
	Handle      string  `json:"handle"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// Studio -.
type Studio struct {
	Channel  Channel       `json:"channel"`
	Settings Settings      `json:"settings"`
	Current  *StudioStream `json:"current"`
}

// StudioStream -.
type StudioStream struct {
	StreamID      string         `json:"streamId"`
	Status        string         `json:"status"`
	StartedAt     *time.Time     `json:"startedAt"`
	InterruptedAt *time.Time     `json:"interruptedAt"`
	ManifestURL   string         `json:"manifestUrl"`
	Health        *entity.Health `json:"health"`
}

// Stream -.
type Stream struct {
	ID           string     `json:"id"`
	ChannelID    string     `json:"channelId"`
	Title        string     `json:"title"`
	Tags         []string   `json:"tags"`
	Language     string     `json:"language"`
	IsMature     bool       `json:"isMature"`
	Status       string     `json:"status"`
	StartedAt    *time.Time `json:"startedAt"`
	EndedAt      *time.Time `json:"endedAt"`
	EndReason    *string    `json:"endReason"`
	ManifestURL  string     `json:"manifestUrl"`
	ThumbnailURL string     `json:"thumbnailUrl"`
	VODID        *string    `json:"vodId"`
	// ViewerCount is the number of people watching right now; null when unknown or not on air.
	ViewerCount *int         `json:"viewerCount"`
	Channel     *Channel     `json:"channel"`
	Category    *CategoryRef `json:"category"`
	// ScheduleID and ScheduledStartAt are set for a stream that comes from a schedule (status SCHEDULED, CANCELLED,
	// or LIVE / ENDED once attached).
	ScheduleID       *string    `json:"scheduleId"`
	ScheduledStartAt *time.Time `json:"scheduledStartAt"`
}

// StreamList -.
type StreamList struct {
	Items      []Stream `json:"items"`
	NextCursor *string  `json:"nextCursor"`
}

// VOD -.
type VOD struct {
	ID           string       `json:"id"`
	StreamID     string       `json:"streamId"`
	ChannelID    string       `json:"channelId"`
	Title        string       `json:"title"`
	DurationSec  int          `json:"durationSec"`
	ManifestURL  string       `json:"manifestUrl"`
	ThumbnailURL string       `json:"thumbnailUrl"`
	Status       string       `json:"status"`
	Visibility   string       `json:"visibility" enums:"PUBLIC,HIDDEN"`
	IsMature     bool         `json:"isMature"`
	Category     *CategoryRef `json:"category"`
	CreatedAt    time.Time    `json:"createdAt"`
	ExpiresAt    time.Time    `json:"expiresAt"`
	Channel      *Channel     `json:"channel"`
	// Viewer is present only when the request carries a valid token.
	Viewer *VODViewer `json:"viewer,omitempty"`
}

// VODList -.
type VODList struct {
	Items      []VOD   `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// StudioVOD is a VOD in the Studio's content list.
type StudioVOD struct {
	ID           string       `json:"id"`
	StreamID     string       `json:"streamId"`
	Title        string       `json:"title"`
	DurationSec  int          `json:"durationSec"`
	ThumbnailURL string       `json:"thumbnailUrl"`
	ManifestURL  string       `json:"manifestUrl"`
	Status       string       `json:"status" enums:"READY,EXPIRED"`
	Visibility   string       `json:"visibility" enums:"PUBLIC,HIDDEN"`
	Category     *CategoryRef `json:"category"`
	CreatedAt    time.Time    `json:"createdAt"`
	ExpiresAt    time.Time    `json:"expiresAt"`
}

// StudioVODList -.
type StudioVODList struct {
	Items      []StudioVOD `json:"items"`
	NextCursor *string     `json:"nextCursor"`
}

// DashboardCurrent is the broadcast on air.
type DashboardCurrent struct {
	StreamID    string     `json:"streamId"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"startedAt"`
	ViewerCount *int       `json:"viewerCount"`
}

// RecentStream is an ended broadcast.
type RecentStream struct {
	StreamID      string     `json:"streamId"`
	Title         string     `json:"title"`
	StartedAt     *time.Time `json:"startedAt"`
	EndedAt       *time.Time `json:"endedAt"`
	DurationSec   int64      `json:"durationSec"`
	EndReason     *string    `json:"endReason"`
	PeakViewers   int        `json:"peakViewers"`
	VODID         *string    `json:"vodId"`
	VODVisibility *string    `json:"vodVisibility"`
}

// DashboardStats -.
type DashboardStats struct {
	Range          string `json:"range" enums:"7d,28d"`
	StreamCount    int    `json:"streamCount"`
	LiveSeconds    int64  `json:"liveSeconds"`
	VODReadyPublic int    `json:"vodReadyPublic"`
	VODReadyHidden int    `json:"vodReadyHidden"`
}

// Dashboard is stream-svc's half of the Studio overview.
type Dashboard struct {
	Current       *DashboardCurrent `json:"current"`
	RecentStreams []RecentStream    `json:"recentStreams"`
	Stats         DashboardStats    `json:"stats"`
}
