// Package events consumes creator approvals and stream lifecycle events from JetStream.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

// Durable is the JetStream consumer name of channel-svc.
const Durable = "channel-svc"

// Types consumed by channel-svc.
var Types = []string{ //nolint:gochecknoglobals // static list
	events.TypeCreatorApproved, events.TypeStreamStarted, events.TypeStreamEnded,
}

// Handler routes one event to the sync use case.
func Handler(sync usecase.Sync) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		if err := checkEventID(e); err != nil {
			return err
		}

		switch e.Type {
		case events.TypeCreatorApproved:
			in, err := decodeCreatorApproved(e)
			if err != nil {
				return err
			}

			return sync.CreatorApproved(ctx, e.ID, in)
		case events.TypeStreamStarted:
			var p events.StreamStarted
			if err := decodeStream(e, &p, &p.ChannelID, &p.StreamID); err != nil {
				return err
			}

			return sync.StreamStarted(ctx, e.ID, p.ChannelID, p.StreamID)
		case events.TypeStreamEnded:
			var p events.StreamEnded
			if err := decodeStream(e, &p, &p.ChannelID, &p.StreamID); err != nil {
				return err
			}

			return sync.StreamEnded(ctx, e.ID, p.ChannelID, p.StreamID)
		}

		return nil
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

func decodeCreatorApproved(e eventbus.Event) (usecase.CreatorApproved, error) {
	var p events.CreatorApproved
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return usecase.CreatorApproved{}, fmt.Errorf("%w: %s: %w", eventbus.ErrSkip, e.Type, err)
	}

	if _, err := uuid.Parse(p.UserID); err != nil {
		return usecase.CreatorApproved{}, fmt.Errorf("%w: %s: bad userId", eventbus.ErrSkip, e.Type)
	}

	if strings.TrimSpace(p.Username) == "" {
		return usecase.CreatorApproved{}, fmt.Errorf("%w: %s: empty username", eventbus.ErrSkip, e.Type)
	}

	return usecase.CreatorApproved{UserID: p.UserID, Username: p.Username, DisplayName: p.DisplayName}, nil
}

// decodeStream rejects payloads that can never be processed so they are not redelivered forever.
func decodeStream(e eventbus.Event, into any, channelID, streamID *string) error {
	if err := json.Unmarshal(e.Payload, into); err != nil {
		return fmt.Errorf("%w: %s: %w", eventbus.ErrSkip, e.Type, err)
	}

	if _, err := uuid.Parse(*channelID); err != nil {
		return fmt.Errorf("%w: %s: bad channelId", eventbus.ErrSkip, e.Type)
	}

	if _, err := uuid.Parse(*streamID); err != nil {
		return fmt.Errorf("%w: %s: bad streamId", eventbus.ErrSkip, e.Type)
	}

	return nil
}
