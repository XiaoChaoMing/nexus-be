// Package events publishes media-svc events (contract: pkg/events).
package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
)

// Publisher -.
type Publisher interface {
	Publish(ctx context.Context, eventType string, payload any) error
}

// Log writes events to the log only (phase L1 / no NATS configured).
type Log struct {
	l logger.Interface
}

// NewLog -.
func NewLog(l logger.Interface) *Log {
	return &Log{l: l}
}

// Publish -.
func (p *Log) Publish(_ context.Context, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	p.l.Info("event %s %s", eventType, b)

	return nil
}

// BusPublisher is satisfied by *eventbus.Bus.
type BusPublisher interface {
	Publish(ctx context.Context, eventType, id string, payload []byte) error
}

// Bus publishes to JetStream with a fresh event id. media-svc has no database, so there is no outbox:
// a failed publish is returned to the caller, which logs it (stream-svc's grace timeout covers a lost stop).
type Bus struct {
	bus BusPublisher
	l   logger.Interface
}

// NewBus -.
func NewBus(b BusPublisher, l logger.Interface) *Bus {
	return &Bus{bus: b, l: l}
}

// Publish -.
func (p *Bus) Publish(ctx context.Context, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("events - marshal %s: %w", eventType, err)
	}

	if err := p.bus.Publish(ctx, eventType, uuid.NewString(), b); err != nil {
		return err
	}

	p.l.Debug("event %s %s", eventType, b)

	return nil
}
