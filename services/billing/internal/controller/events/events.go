// Package events consumes the channel events billing-svc keeps a copy of (durable billing-svc).
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/billing/internal/usecase"
)

// Durable is the JetStream consumer name of billing-svc (DeliverAll).
const Durable = "billing-svc"

// Types consumed by billing-svc.
func Types() []string {
	return []string{events.TypeChannelCreated, events.TypeChannelUpdated, events.TypeChannelSuspended, events.TypeChannelRestored}
}

// Handler routes one channel event to the copy; idempotent on the event id (processed_events).
func Handler(replica usecase.Replica) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		if uuid.Validate(e.ID) != nil {
			return fmt.Errorf("%w: %s: no event id", eventbus.ErrSkip, e.Type)
		}

		at := e.OccurredAt
		if at.IsZero() {
			at = time.Now()
		}

		switch e.Type {
		case events.TypeChannelCreated:
			var p events.ChannelCreated
			if err := decode(e, &p, func() []string { return []string{p.ChannelID, p.OwnerID} }); err != nil {
				return err
			}

			return replica.ChannelCreated(ctx, e.ID, at, p)
		case events.TypeChannelUpdated:
			var p events.ChannelUpdated
			if err := decode(e, &p, func() []string { return []string{p.ChannelID, p.OwnerID} }); err != nil {
				return err
			}

			return replica.ChannelUpdated(ctx, e.ID, at, p)
		case events.TypeChannelSuspended, events.TypeChannelRestored:
			var p events.ChannelSuspended
			if err := decode(e, &p, func() []string { return []string{p.ChannelID} }); err != nil {
				return err
			}

			return replica.ChannelStatus(ctx, e.ID, at, p.ChannelID, e.Type == events.TypeChannelSuspended)
		}

		return fmt.Errorf("%w: unexpected type %s", eventbus.ErrSkip, e.Type)
	}
}

// decode unmarshals the payload and checks the ids ids() reads from it; anything else is skipped (a retry
// would fail the same way).
func decode[T any](e eventbus.Event, into *T, ids func() []string) error {
	if err := json.Unmarshal(e.Payload, into); err != nil {
		return fmt.Errorf("%w: %s: %w", eventbus.ErrSkip, e.Type, err)
	}

	for _, id := range ids() {
		if uuid.Validate(id) != nil {
			return fmt.Errorf("%w: %s: bad id %q", eventbus.ErrSkip, e.Type, id)
		}
	}

	return nil
}
