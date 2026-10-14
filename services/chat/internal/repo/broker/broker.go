// Package broker fans realtime events out between chat-svc instances over core NATS.
//
// Subjects are "rt.<topic>" (rt.chat:<uuid>), deliberately outside "nxl.>": the JetStream stream NXL
// captures every nxl.* subject and would persist each chat message for a week.
package broker

import (
	"context"
	"fmt"

	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/nats-io/nats.go"
)

// SubjectPrefix is prepended to a topic to form its NATS subject.
const SubjectPrefix = "rt."

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
