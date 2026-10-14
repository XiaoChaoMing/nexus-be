// Package fanout publishes realtime events: a Redis seq and resume-buffer entry first, then core NATS so
// every instance with listeners delivers it. The publishing instance receives its own event through NATS
// too, so all sockets see one ordered stream.
package fanout

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
)

// Publisher -.
type Publisher struct {
	seq    repo.SeqStore
	broker repo.Broker
	l      logger.Interface
	now    func() time.Time
}

// New -.
func New(seq repo.SeqStore, broker repo.Broker, l logger.Interface) *Publisher {
	return &Publisher{seq: seq, broker: broker, l: l, now: time.Now}
}

// Publish sends one event of type typ with data (JSON-encoded) on topic. It fails only when the event
// could not be numbered and buffered: once buffered, a lost NATS publish is recovered by the receivers,
// which see the seq gap on the next event (or the client's resume) and read the buffer.
func (p *Publisher) Publish(ctx context.Context, topic entity.Topic, typ string, data any) (entity.Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return entity.Event{}, fmt.Errorf("fanout - marshal %s: %w", typ, err)
	}

	ev, err := p.seq.Append(ctx, entity.Event{Topic: topic.String(), Type: typ, Data: raw, TS: p.now().UTC()})
	if err != nil {
		return entity.Event{}, fmt.Errorf("fanout - append %s: %w", typ, err)
	}

	payload, err := json.Marshal(ev)
	if err != nil {
		return entity.Event{}, fmt.Errorf("fanout - marshal event: %w", err)
	}

	if err = p.broker.Publish(ctx, ev.Topic, payload); err != nil {
		p.l.Warn("fanout - publish %s seq %d on %s: %v", typ, ev.Seq, ev.Topic, err)
	}

	return ev, nil
}
