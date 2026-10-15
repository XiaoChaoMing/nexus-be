// Package eventbus publishes and consumes NexusLive domain events on NATS JetStream.
//
// Every event lives on the single stream "NXL" under subjects "nxl.<type>", e.g. nxl.stream.ended.v1.
// The NATS message id (header Nats-Msg-Id) is the event id: JetStream drops duplicates inside its
// window, and consumers record it in processed_events to stay idempotent across redeliveries.
// Nxl-Occurred-At (RFC 3339) and Nxl-Producer carry when and where the change happened (NXL-00 §11).
package eventbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// StreamName is the JetStream stream that stores every NexusLive event.
	StreamName = "NXL"
	// SubjectPrefix is prepended to an event type to form its subject.
	SubjectPrefix = "nxl."
	// HeaderOccurredAt is when the change was committed (RFC 3339, UTC).
	HeaderOccurredAt = "Nxl-Occurred-At"
	// HeaderProducer is the name of the service that published the event.
	HeaderProducer = "Nxl-Producer"

	_maxAge      = 7 * 24 * time.Hour
	_dedupWindow = 2 * time.Minute
	_ackWait     = 30 * time.Second
	// A handler must finish well before JetStream redelivers the message.
	_handlerTimeout = _ackWait / 2
	_maxDeliver     = 20
	_retryDelay     = 2 * time.Second
	_connectWait    = 2 * time.Second
	_reconnectWait  = time.Second
)

// Subject returns the NATS subject of an event type ("stream.ended.v1" → "nxl.stream.ended.v1").
func Subject(eventType string) string {
	return SubjectPrefix + eventType
}

// Bus -.
type Bus struct {
	NC       *nats.Conn
	JS       jetstream.JetStream
	producer string
}

// Connect dials NATS, retrying until ctx expires (NATS may start after the service under compose),
// and makes sure the NXL stream exists.
func Connect(ctx context.Context, url, name string) (*Bus, error) {
	var (
		nc  *nats.Conn
		err error
	)

	for {
		nc, err = nats.Connect(url,
			nats.Name(name),
			nats.Timeout(_connectWait),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(_reconnectWait),
		)
		if err == nil {
			break
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("eventbus - Connect: %w (last error: %w)", ctx.Err(), err)
		case <-time.After(_connectWait):
		}
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()

		return nil, fmt.Errorf("eventbus - jetstream.New: %w", err)
	}

	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       StreamName,
		Subjects:   []string{SubjectPrefix + ">"},
		Storage:    jetstream.FileStorage,
		MaxAge:     _maxAge,
		Duplicates: _dedupWindow,
	})
	if err != nil {
		nc.Close()

		return nil, fmt.Errorf("eventbus - CreateOrUpdateStream: %w", err)
	}

	return &Bus{NC: nc, JS: js, producer: name}, nil
}

// Close drains the connection.
func (b *Bus) Close() {
	_ = b.NC.Drain() //nolint:errcheck // shutting down
}

// Publish stores one event that happens now; id becomes Nats-Msg-Id.
func (b *Bus) Publish(ctx context.Context, eventType, id string, payload []byte) error {
	return b.PublishAt(ctx, eventType, id, time.Now(), payload)
}

// PublishAt stores one event that was committed at occurredAt (the outbox passes the row's created_at).
func (b *Bus) PublishAt(ctx context.Context, eventType, id string, occurredAt time.Time, payload []byte) error {
	msg := nats.NewMsg(Subject(eventType))
	msg.Data = payload
	msg.Header.Set(jetstream.MsgIDHeader, id)
	msg.Header.Set(HeaderOccurredAt, occurredAt.UTC().Format(time.RFC3339Nano))

	if b.producer != "" {
		msg.Header.Set(HeaderProducer, b.producer)
	}

	if _, err := b.JS.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("eventbus - Publish %s: %w", eventType, err)
	}

	return nil
}

// Event is a consumed message.
type Event struct {
	ID   string
	Type string
	// OccurredAt is zero when the publisher did not set Nxl-Occurred-At.
	OccurredAt time.Time
	Producer   string
	Payload    []byte
}

// Handler processes one event. A nil error acks it; an error makes JetStream redeliver it later.
type Handler func(ctx context.Context, e Event) error

// ErrSkip acks an event the handler cannot ever process (bad payload): retrying would not help.
var ErrSkip = errors.New("eventbus: skip event")

// ConsumeOption changes the configuration of a durable consumer before it is created.
type ConsumeOption func(*jetstream.ConsumerConfig)

// DeliverNew makes a durable created for the first time start at the next message instead of the stream's
// start. It has no effect on an existing durable (JetStream does not allow changing the deliver policy).
func DeliverNew() ConsumeOption {
	return func(c *jetstream.ConsumerConfig) { c.DeliverPolicy = jetstream.DeliverNewPolicy }
}

// Consume attaches a durable consumer that receives the given event types and runs h for each.
// It returns a stop function.
func (b *Bus) Consume(ctx context.Context, durable string, types []string, h Handler, onErr func(Event, error),
	opts ...ConsumeOption,
) (func(), error) {
	subjects := make([]string, len(types))
	for i, t := range types {
		subjects[i] = Subject(t)
	}

	cfg := jetstream.ConsumerConfig{
		Durable:        durable,
		FilterSubjects: subjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		AckWait:        _ackWait,
		MaxDeliver:     _maxDeliver,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	}

	for _, o := range opts {
		o(&cfg)
	}

	if err := b.keepDeliverPolicy(ctx, &cfg); err != nil {
		return nil, err
	}

	cons, err := b.JS.CreateOrUpdateConsumer(ctx, StreamName, cfg)
	if err != nil {
		return nil, fmt.Errorf("eventbus - CreateOrUpdateConsumer %s: %w", durable, err)
	}

	cc, err := cons.Consume(deliver(h, onErr))
	if err != nil {
		return nil, fmt.Errorf("eventbus - Consume %s: %w", durable, err)
	}

	return cc.Stop, nil
}

// deliver runs h for one message and acks, terminates (ErrSkip) or naks it.
func deliver(h Handler, onErr func(Event, error)) jetstream.MessageHandler {
	return func(m jetstream.Msg) {
		hdr := m.Headers()
		e := Event{
			ID:       hdr.Get(jetstream.MsgIDHeader),
			Type:     m.Subject()[len(SubjectPrefix):],
			Producer: hdr.Get(HeaderProducer),
			Payload:  m.Data(),
		}

		if at, perr := time.Parse(time.RFC3339Nano, hdr.Get(HeaderOccurredAt)); perr == nil {
			e.OccurredAt = at
		}

		hctx, cancel := context.WithTimeout(context.Background(), _handlerTimeout)
		defer cancel()

		herr := h(hctx, e)

		switch {
		case herr == nil:
			_ = m.Ack() //nolint:errcheck // redelivered if lost; handlers are idempotent
		case errors.Is(herr, ErrSkip):
			if onErr != nil {
				onErr(e, herr)
			}

			_ = m.Term() //nolint:errcheck // same
		default:
			if onErr != nil {
				onErr(e, herr)
			}

			_ = m.NakWithDelay(_retryDelay) //nolint:errcheck // same
		}
	}
}

// keepDeliverPolicy reuses the deliver policy of an existing durable: an update that changes it is refused.
func (b *Bus) keepDeliverPolicy(ctx context.Context, cfg *jetstream.ConsumerConfig) error {
	if cfg.DeliverPolicy == jetstream.DeliverAllPolicy {
		return nil
	}

	cons, err := b.JS.Consumer(ctx, StreamName, cfg.Durable)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("eventbus - Consumer %s: %w", cfg.Durable, err)
	}

	existing := cons.CachedInfo().Config
	cfg.DeliverPolicy, cfg.OptStartSeq, cfg.OptStartTime = existing.DeliverPolicy, existing.OptStartSeq, existing.OptStartTime

	return nil
}

// DeleteConsumer removes a durable; a durable that does not exist is not an error.
func (b *Bus) DeleteConsumer(ctx context.Context, durable string) error {
	err := b.JS.DeleteConsumer(ctx, StreamName, durable)
	if err != nil && !errors.Is(err, jetstream.ErrConsumerNotFound) {
		return fmt.Errorf("eventbus - DeleteConsumer %s: %w", durable, err)
	}

	return nil
}
