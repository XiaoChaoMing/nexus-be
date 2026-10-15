// Package broker fans realtime events out between chat-svc instances over core NATS.
//
// Subjects are "rt.<topic>" (rt.chat:<uuid>), deliberately outside "nxl.>": the JetStream stream NXL
// captures every nxl.* subject and would persist each chat message for a week.
package broker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/nats-io/nats.go"
)

// SubjectPrefix is prepended to a topic to form its NATS subject.
const SubjectPrefix = "rt."

// ControlSubject carries orders every instance applies to its own connections (close a locked user's
// sockets). Like the realtime subjects it stays outside "nxl.>" so JetStream does not store it.
const ControlSubject = "rtctl.user"

// Broker implements repo.Broker.
type Broker struct {
	nc *nats.Conn
}

var _ repo.Broker = (*Broker)(nil)

// New -.
func New(nc *nats.Conn) *Broker {
	return &Broker{nc: nc}
}

// Publish -.
func (b *Broker) Publish(_ context.Context, topic string, payload []byte) error {
	if err := b.nc.Publish(SubjectPrefix+topic, payload); err != nil {
		return fmt.Errorf("broker - Publish: %w", err)
	}

	return nil
}

// Subscribe -.
func (b *Broker) Subscribe(topic string, fn func([]byte)) (func(), error) {
	sub, err := b.nc.Subscribe(SubjectPrefix+topic, func(m *nats.Msg) { fn(m.Data) })
	if err != nil {
		return nil, fmt.Errorf("broker - Subscribe: %w", err)
	}

	return func() { _ = sub.Unsubscribe() }, nil //nolint:errcheck // connection closing or already gone
}

// PublishControl -.
func (b *Broker) PublishControl(_ context.Context, c entity.Control) error {
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("broker - PublishControl - marshal: %w", err)
	}

	if err = b.nc.Publish(ControlSubject, data); err != nil {
		return fmt.Errorf("broker - PublishControl: %w", err)
	}

	return nil
}

// SubscribeControl -.
func (b *Broker) SubscribeControl(fn func(entity.Control)) (func(), error) {
	sub, err := b.nc.Subscribe(ControlSubject, func(m *nats.Msg) {
		var c entity.Control
		if json.Unmarshal(m.Data, &c) == nil && c.UserID != "" {
			fn(c)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("broker - SubscribeControl: %w", err)
	}

	return func() { _ = sub.Unsubscribe() }, nil //nolint:errcheck // connection closing or already gone
}
