// Package events consumes media-svc ingest events, channel-svc channel events and chat-svc peak viewer
// counts from JetStream.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

// Durable is the JetStream consumer name of stream-svc.
const Durable = "stream-svc"

// Limits of creator_channels.handle and display_name.
const (
	_maxHandle      = 25
	_maxDisplayName = 50
)

// Types consumed by stream-svc.
var Types = []string{ //nolint:gochecknoglobals // static list
	events.TypeIngestStarted, events.TypeIngestStopped, events.TypeIngestHealth, events.TypeVODProcessed,
	events.TypeChannelCreated, events.TypeStreamPeakViewers,
}

// Handler routes one event to the ingest or channel use case.
func Handler(ingest usecase.Ingest, channels usecase.ChannelSync) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		switch e.Type {
		case events.TypeIngestStarted:
			var p events.IngestStarted
			if err := decode(e, &p.StreamID, &p); err != nil {
				return err
			}

			return ingest.IngestStarted(ctx, e.ID, p.StreamID)
		case events.TypeIngestStopped:
			var p events.IngestStopped
			if err := decode(e, &p.StreamID, &p); err != nil {
				return err
			}

			return ingest.IngestStopped(ctx, e.ID, p.StreamID)
		case events.TypeIngestHealth:
			return onHealth(ingest, e)
		case events.TypeVODProcessed:
			return onVODProcessed(ctx, ingest, e)
		case events.TypeChannelCreated:
			return onChannelCreated(ctx, channels, e)
		case events.TypeStreamPeakViewers:
			return onPeakViewers(ctx, ingest, e)
		}

		return nil
	}
}

func onHealth(ingest usecase.Ingest, e eventbus.Event) error {
	var p events.IngestHealth
	if err := decode(e, &p.StreamID, &p); err != nil {
		return err
	}

	ingest.IngestHealth(p.StreamID, entity.Health{
		FPS: p.FPS, BitrateKbps: p.BitrateKbps, DropFrames: p.DroppedFrames,
		Speed: p.Speed, UploadErrors: p.UploadErrors, ReportedAt: time.Now().UTC(),
	})

	return nil
}

func onVODProcessed(ctx context.Context, ingest usecase.Ingest, e eventbus.Event) error {
	var p events.VODProcessed
	if err := decode(e, &p.StreamID, &p); err != nil {
		return err
	}

	if !strings.HasPrefix(p.PlaylistPath, "streams/") {
		return fmt.Errorf("%w: playlist path %q", eventbus.ErrSkip, p.PlaylistPath)
	}

	return ingest.VODProcessed(ctx, e.ID, repo.VODInput{
		StreamID:     p.StreamID,
		DurationSec:  int(math.Round(p.DurationSec)),
		PlaylistPath: p.PlaylistPath,
		ThumbnailURL: p.ThumbnailPath,
	})
}

func onPeakViewers(ctx context.Context, ingest usecase.Ingest, e eventbus.Event) error {
	var p events.StreamPeakViewers
	if err := decode(e, &p.StreamID, &p); err != nil {
		return err
	}

	if p.PeakViewers < 0 {
		return fmt.Errorf("%w: %s: negative peak", eventbus.ErrSkip, e.Type)
	}

	return ingest.PeakViewers(ctx, e.ID, p.StreamID, p.PeakViewers)
}

func onChannelCreated(ctx context.Context, channels usecase.ChannelSync, e eventbus.Event) error {
	var p events.ChannelCreated
	if err := decode(e, &p.ChannelID, &p); err != nil {
		return err
	}

	if _, err := uuid.Parse(p.OwnerID); err != nil {
		return fmt.Errorf("%w: %s: bad ownerId", eventbus.ErrSkip, e.Type)
	}

	if p.Handle == "" || len(p.Handle) > _maxHandle {
		return fmt.Errorf("%w: %s: bad handle", eventbus.ErrSkip, e.Type)
	}

	if n := utf8.RuneCountInString(p.DisplayName); n == 0 || n > _maxDisplayName {
		return fmt.Errorf("%w: %s: bad displayName", eventbus.ErrSkip, e.Type)
	}

	return channels.ChannelCreated(ctx, e.ID, entity.Channel{
		ID: p.ChannelID, OwnerID: p.OwnerID, Handle: p.Handle, DisplayName: p.DisplayName,
	})
}

// decode rejects payloads that can never be processed so they are not redelivered forever.
// id is the payload's key uuid (streamId, or channelId for channel events).
func decode(e eventbus.Event, id *string, into any) error {
	if err := json.Unmarshal(e.Payload, into); err != nil {
		return fmt.Errorf("%w: %s: %w", eventbus.ErrSkip, e.Type, err)
	}

	if _, err := uuid.Parse(*id); err != nil {
		return fmt.Errorf("%w: %s: bad id", eventbus.ErrSkip, e.Type)
	}

	if e.ID != "" {
		if _, err := uuid.Parse(e.ID); err != nil {
			return fmt.Errorf("%w: %s: event id is not a uuid", eventbus.ErrSkip, e.Type)
		}
	}

	return nil
}
