// Package events consumes creator approvals, stream lifecycle and VOD events from JetStream.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

// Durable is the JetStream consumer name of channel-svc.
const Durable = "channel-svc"

// Types consumed by channel-svc.
var Types = []string{ //nolint:gochecknoglobals // static list
	events.TypeCreatorApproved, events.TypeStreamStarted, events.TypeStreamUpdated, events.TypeStreamEnded,
	events.TypeStreamScheduled, events.TypeStreamVODReady, events.TypeVODUpdated, events.TypeVODDeleted,
}

// _vodReady is the VOD status that can be shown publicly.
const (
	_vodReady  = "READY"
	_vodPublic = "PUBLIC"
)

// Handler routes one event to the sync use case.
func Handler(sync usecase.Sync) eventbus.Handler {
	handlers := map[string]eventbus.Handler{
		events.TypeCreatorApproved: onCreatorApproved(sync),
		events.TypeStreamStarted: handle(func(p events.StreamStarted) []string { return []string{p.StreamID, p.ChannelID} },
			func(ctx context.Context, id string, p events.StreamStarted) error {
				return sync.StreamStarted(ctx, id, liveOf(p.StreamID, p.ChannelID, p.Title, p.CategoryID, p.Tags, p.Language,
					p.IsMature).withStart(p.StartedAt))
			}),
		events.TypeStreamUpdated: handle(func(p events.StreamUpdated) []string { return []string{p.StreamID, p.ChannelID} },
			func(ctx context.Context, id string, p events.StreamUpdated) error {
				return sync.StreamUpdated(ctx, id, liveOf(p.StreamID, p.ChannelID, p.Title, p.CategoryID, p.Tags, p.Language, p.IsMature).s)
			}),
		events.TypeStreamEnded: handle(func(p events.StreamEnded) []string { return []string{p.StreamID, p.ChannelID} },
			func(ctx context.Context, id string, p events.StreamEnded) error {
				return sync.StreamEnded(ctx, id, p.ChannelID, p.StreamID)
			}),
		events.TypeStreamScheduled: handle(func(p events.StreamScheduled) []string { return []string{p.StreamID, p.ScheduleID} },
			func(ctx context.Context, id string, p events.StreamScheduled) error {
				return sync.StreamScheduled(ctx, id, p)
			}),
		events.TypeStreamVODReady: handle(func(p events.StreamVODReady) []string { return []string{p.VODID, p.StreamID, p.ChannelID} },
			func(ctx context.Context, id string, p events.StreamVODReady) error {
				return sync.VODChanged(ctx, id, entity.VODDoc{
					VODID: p.VODID, StreamID: p.StreamID, ChannelID: p.ChannelID,
					Title: p.Title, ThumbnailURL: p.ThumbnailURL, CategoryID: validID(p.CategoryID), IsMature: p.IsMature,
					ExpiresAt: p.ExpiresAt, Public: !p.ExpiresAt.IsZero(),
				})
			}),
		events.TypeVODUpdated: handle(func(p events.VODUpdated) []string { return []string{p.VODID, p.StreamID, p.ChannelID} },
			func(ctx context.Context, id string, p events.VODUpdated) error {
				return sync.VODChanged(ctx, id, entity.VODDoc{
					VODID: p.VODID, StreamID: p.StreamID, ChannelID: p.ChannelID,
					Title: p.Title, ThumbnailURL: p.ThumbnailURL, CategoryID: validID(p.CategoryID), IsMature: p.IsMature,
					ExpiresAt: p.ExpiresAt, Public: p.Status == _vodReady && p.Visibility == _vodPublic,
				})
			}),
		events.TypeVODDeleted: handle(func(p events.VODDeleted) []string { return []string{p.VODID} },
			func(ctx context.Context, id string, p events.VODDeleted) error {
				return sync.VODDeleted(ctx, id, p.VODID)
			}),
	}

	return func(ctx context.Context, e eventbus.Event) error {
		if h, ok := handlers[e.Type]; ok {
			return h(ctx, e)
		}

		return nil
	}
}

type liveBuilder struct{ s entity.LiveStream }

func liveOf(streamID, channelID, title string, categoryID *string, tags []string, lang string, mature bool) liveBuilder {
	if lang == "" {
		lang = "vi"
	}

	if tags == nil {
		tags = []string{}
	}

	return liveBuilder{s: entity.LiveStream{
		StreamID: streamID, ChannelID: channelID, Title: title, CategoryID: validID(categoryID),
		Tags: tags, Language: lang, IsMature: mature,
	}}
}

// withStart: events published before startedAt existed carry a zero time; the event's arrival is close enough.
func (b liveBuilder) withStart(t time.Time) entity.LiveStream {
	if t.IsZero() {
		t = time.Now()
	}

	b.s.StartedAt = t.UTC()

	return b.s
}

// validID drops an id that is not a uuid (it could never match a category).
func validID(id *string) *string {
	if id == nil {
		return nil
	}

	u, err := uuid.Parse(*id)
	if err != nil {
		return nil
	}

	s := u.String()

	return &s
}

// handle decodes the payload as T and checks its uuids (ids) and the event id before calling fn; payloads that
// can never be processed are skipped instead of being redelivered forever.
func handle[T any](ids func(T) []string, fn func(ctx context.Context, eventID string, p T) error) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		if err := checkEventID(e); err != nil {
			return err
		}

		var p T
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("%w: %s: %w", eventbus.ErrSkip, e.Type, err)
		}

		for _, id := range ids(p) {
			if _, err := uuid.Parse(id); err != nil {
				return fmt.Errorf("%w: %s: bad id", eventbus.ErrSkip, e.Type)
			}
		}

		return fn(ctx, e.ID, p)
	}
}

// checkEventID: processed_events.event_id is a uuid, so any other id can never be recorded.
func checkEventID(e eventbus.Event) error {
	if e.ID == "" {
		return nil
	}

	if _, err := uuid.Parse(e.ID); err != nil {
		return fmt.Errorf("%w: %s: event id is not a uuid", eventbus.ErrSkip, e.Type)
	}

	return nil
}

func onCreatorApproved(sync usecase.Sync) eventbus.Handler {
	return handle(func(p events.CreatorApproved) []string { return []string{p.UserID} },
		func(ctx context.Context, id string, p events.CreatorApproved) error {
			if strings.TrimSpace(p.Username) == "" {
				return fmt.Errorf("%w: %s: empty username", eventbus.ErrSkip, events.TypeCreatorApproved)
			}

			return sync.CreatorApproved(ctx, id, usecase.CreatorApproved{UserID: p.UserID, Username: p.Username, DisplayName: p.DisplayName})
		})
}
