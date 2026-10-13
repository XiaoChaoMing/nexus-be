// Package usecase declares stream-svc use cases.
package usecase

import (
	"context"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
)

type (
	// Studio is what a creator does with their own channel. channelID always comes from Channel.
	Studio interface {
		// Channel resolves the caller's channel; entity.ErrNotCreator when they own none.
		Channel(ctx context.Context, ownerID string) (entity.Channel, error)
		CreateKey(ctx context.Context, channelID string) (key string, err error)
		KeyInfo(ctx context.Context, channelID string) (entity.StreamKeyInfo, error)
		Overview(ctx context.Context, channelID string) (StudioOverview, error)
		UpdateSettings(ctx context.Context, s entity.Settings) (entity.Settings, error)
		End(ctx context.Context, channelID string) error
	}

	// Ingest handles media-svc: the authorize call and ingest events.
	Ingest interface {
		Authorize(ctx context.Context, streamKey, clientIP string) (entity.Grant, error)
		IngestStarted(ctx context.Context, eventID, streamID string) error
		IngestStopped(ctx context.Context, eventID, streamID string) error
		IngestHealth(streamID string, h entity.Health)
		VODProcessed(ctx context.Context, eventID string, v repo.VODInput) error
		// ExpireInterrupted ends streams whose grace period ran out; returns how many.
		ExpireInterrupted(ctx context.Context) (int, error)
		// ExpireVODs marks VODs past their retention as EXPIRED; returns how many.
		ExpireVODs(ctx context.Context) (int64, error)
	}

	// ChannelSync keeps the local copy of channel-svc's channels.
	ChannelSync interface {
		ChannelCreated(ctx context.Context, eventID string, c entity.Channel) error
	}

	// Observer receives counters for /metrics; the use case never depends on a metrics library.
	Observer interface {
		Authorize(result string)
		Transition(to entity.Status)
		OnAir(n int)
	}

	// Catalog is the public read side; streams and VODs carry their channel when it is known.
	Catalog interface {
		ListLive(ctx context.Context, cursor string, limit int) (Page, error)
		Get(ctx context.Context, streamID string) (entity.Stream, error)
		GetVOD(ctx context.Context, vodID string) (entity.VOD, error)
	}

	// PlaybackInput identifies the viewer opening a stream or VOD.
	PlaybackInput struct {
		Target entity.PlaybackTarget
		// UserID is empty for guests; AnonID is the guest's browser id (generated when empty).
		UserID            string
		AnonID            string
		AcknowledgeMature bool
	}

	// Viewer is what a viewer does around playback: open a session past the age gate and resume VODs.
	Viewer interface {
		Open(ctx context.Context, in PlaybackInput) (entity.PlaybackGrant, error)
		SaveProgress(ctx context.Context, p entity.WatchProgress, keepFurther bool) error
		// Progress returns nil when the user never watched the VOD.
		Progress(ctx context.Context, userID, vodID string) (*int, error)
		ContinueWatching(ctx context.Context, userID string, limit int) ([]entity.ContinueItem, error)
		// PurgeSessions removes expired playback sessions; returns how many.
		PurgeSessions(ctx context.Context) (int64, error)
	}

	// StudioOverview -.
	StudioOverview struct {
		Settings entity.Settings
		Current  *entity.Stream
		Health   *entity.Health
	}

	// Page -.
	Page struct {
		Items      []entity.Stream
		NextCursor string
	}
)
