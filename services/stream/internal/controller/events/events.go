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
	_maxTitle       = 140
)

// Types consumed by stream-svc.
var Types = []string{ //nolint:gochecknoglobals // static list
	events.TypeIngestStarted, events.TypeIngestStopped, events.TypeIngestHealth, events.TypeVODProcessed,
	events.TypeChannelCreated, events.TypeChannelUpdated, events.TypeStreamPeakViewers,
	events.TypeScheduleUpdated, events.TypeScheduleOccurrenceUpcoming, events.TypeScheduleOccurrenceChanged,
	events.TypeStreamTakedown, events.TypeChannelSuspended, events.TypeChannelRestored,
}

// Handler routes one event to the ingest, channel, schedule or safety use case.
func Handler(ingest usecase.Ingest, channels usecase.ChannelSync, schedules usecase.Schedules, safety usecase.Safety) eventbus.Handler {
	base := ingestHandler(ingest, channels, schedules)

	return func(ctx context.Context, e eventbus.Event) error {
		switch e.Type {
		case events.TypeStreamTakedown, events.TypeChannelSuspended, events.TypeChannelRestored:
			return onSafety(ctx, safety, e)
		}

		return base(ctx, e)
	}
}

func ingestHandler(ingest usecase.Ingest, channels usecase.ChannelSync, schedules usecase.Schedules) eventbus.Handler {
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
			return onChannel(ctx, e, channels.ChannelCreated)
		case events.TypeChannelUpdated:
			return onChannel(ctx, e, channels.ChannelUpdated)
		case events.TypeStreamPeakViewers:
			return onPeakViewers(ctx, ingest, e)
		}

		return onSchedule(ctx, schedules, e)
	}
}

// onSafety needs the event id: processed_events applies each action once.
func onSafety(ctx context.Context, safety usecase.Safety, e eventbus.Event) error {
	if e.ID == "" {
		return fmt.Errorf("%w: %s: no event id", eventbus.ErrSkip, e.Type)
	}

	if e.Type == events.TypeStreamTakedown {
		var p events.StreamTakedown
		if err := decode(e, &p.StreamID, &p); err != nil {
			return err
		}

		return safety.Takedown(ctx, e.ID, p)
	}

	var p events.ChannelSuspended
	if err := decode(e, &p.ChannelID, &p); err != nil {
		return err
	}

	if e.Type == events.TypeChannelSuspended {
		return safety.ChannelSuspended(ctx, e.ID, p)
	}

	return safety.ChannelRestored(ctx, e.ID, p)
}

func onSchedule(ctx context.Context, schedules usecase.Schedules, e eventbus.Event) error {
	switch e.Type {
	case events.TypeScheduleOccurrenceUpcoming:
		var p events.ScheduleOccurrenceUpcoming
		if err := decodeSchedule(e, &p, &p.ScheduleID, &p.ChannelID); err != nil {
			return err
		}

		if p.OccurrenceAt.IsZero() || utf8.RuneCountInString(p.Title) > _maxTitle {
			return fmt.Errorf("%w: %s: bad occurrence", eventbus.ErrSkip, e.Type)
		}

		p.CategoryID = validID(p.CategoryID)

		return schedules.OccurrenceUpcoming(ctx, e.ID, p)
	case events.TypeScheduleOccurrenceChanged:
		var p events.ScheduleOccurrenceChanged
		if err := decodeSchedule(e, &p, &p.ScheduleID); err != nil {
			return err
		}

		return schedules.OccurrenceChanged(ctx, e.ID, p)
	case events.TypeScheduleUpdated:
		var p events.ScheduleUpdated
		if err := decodeSchedule(e, &p, &p.ScheduleID); err != nil {
			return err
		}

		if utf8.RuneCountInString(p.Title) > _maxTitle {
			return fmt.Errorf("%w: %s: bad title", eventbus.ErrSkip, e.Type)
		}

		p.CategoryID = validID(p.CategoryID)

		return schedules.ScheduleUpdated(ctx, e.ID, p)
	}

	return nil
}

// decodeSchedule decodes a schedule event and checks its uuids.
func decodeSchedule(e eventbus.Event, into any, ids ...*string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: %s: no id", eventbus.ErrSkip, e.Type)
	}

	if err := decode(e, ids[0], into); err != nil {
		return err
	}

	for _, id := range ids[1:] {
		if _, err := uuid.Parse(*id); err != nil {
			return fmt.Errorf("%w: %s: bad id", eventbus.ErrSkip, e.Type)
		}
	}

	return nil
}

// validID drops an id that is not a uuid.
func validID(id *string) *string {
	if id == nil {
		return nil
	}

	if _, err := uuid.Parse(*id); err != nil {
		return nil
	}

	return id
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

// onChannel applies channel.created and channel.updated (same fields; updated adds the avatar).
func onChannel(ctx context.Context, e eventbus.Event, apply func(context.Context, string, entity.Channel) error) error {
	var p events.ChannelUpdated
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

	return apply(ctx, e.ID, entity.Channel{
		ID: p.ChannelID, OwnerID: p.OwnerID, Handle: p.Handle, DisplayName: p.DisplayName, AvatarURL: p.AvatarURL,
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
