package eventbus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrNoEvent is returned by Puller.Next when no event arrived within the wait.
var ErrNoEvent = errors.New("eventbus: no event")

// AckWait sets how long JetStream waits for an ack (or Message.InProgress) before redelivering.
func AckWait(d time.Duration) ConsumeOption {
	return func(c *jetstream.ConsumerConfig) { c.AckWait = d }
}

// MaxDeliver sets how many times a message is delivered before JetStream gives up on it (-1 = no limit).
func MaxDeliver(n int) ConsumeOption {
	return func(c *jetstream.ConsumerConfig) { c.MaxDeliver = n }
}

// MaxAckPending caps the messages delivered to the durable and not yet acked.
func MaxAckPending(n int) ConsumeOption {
	return func(c *jetstream.ConsumerConfig) { c.MaxAckPending = n }
}

// Message is a pulled event whose acknowledgement the caller controls (long-running work).
type Message struct {
	Event
	// Delivered is how many times JetStream delivered this message, this delivery included.
	Delivered uint64
	// Seq is the message's sequence in the stream: unique per stored event.
	Seq uint64

	msg jetstream.Msg
}

// Ack marks the event as processed.
func (m *Message) Ack() error {
	if err := m.msg.Ack(); err != nil {
		return fmt.Errorf("eventbus - Ack: %w", err)
	}

	return nil
}

// Term drops an event that can never be processed.
func (m *Message) Term() error {
	if err := m.msg.Term(); err != nil {
		return fmt.Errorf("eventbus - Term: %w", err)
	}

	return nil
}

// NakWithDelay asks JetStream to redeliver the event after delay (counts as a delivery).
func (m *Message) NakWithDelay(delay time.Duration) error {
	if err := m.msg.NakWithDelay(delay); err != nil {
		return fmt.Errorf("eventbus - NakWithDelay: %w", err)
	}

	return nil
}

// InProgress resets the ack timer of an event that is still being worked on.
func (m *Message) InProgress() error {
	if err := m.msg.InProgress(); err != nil {
		return fmt.Errorf("eventbus - InProgress: %w", err)
	}

	return nil
}

// Puller fetches events one at a time from a durable consumer; the caller acks each Message.
type Puller struct {
	cons jetstream.Consumer
}

// Pull creates or updates a durable consumer for the given event types without starting delivery:
// events are fetched with Puller.Next only when the caller has room for one.
func (b *Bus) Pull(ctx context.Context, durable string, types []string, opts ...ConsumeOption) (*Puller, error) {
	cons, err := b.createConsumer(ctx, durable, types, opts)
	if err != nil {
		return nil, err
	}

	return &Puller{cons: cons}, nil
}

// Next waits up to maxWait for the next event. It returns ErrNoEvent when none arrived and ctx's error
// when ctx ends first.
func (p *Puller) Next(ctx context.Context, maxWait time.Duration) (*Message, error) {
	waitCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	m, err := p.cons.Next(jetstream.FetchContext(waitCtx))
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("eventbus - Next: %w", ctx.Err())
		}

		if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, jetstream.ErrNoMessages) {
			return nil, ErrNoEvent
		}

		return nil, fmt.Errorf("eventbus - Next: %w", err)
	}

	out := &Message{Event: eventOf(m), msg: m}

	if meta, merr := m.Metadata(); merr == nil {
		out.Delivered = meta.NumDelivered
		out.Seq = meta.Sequence.Stream
	}

	return out, nil
}
