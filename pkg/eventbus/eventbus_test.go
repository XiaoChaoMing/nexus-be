package eventbus_test

import (
	"context"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/stretchr/testify/require"
)

// TestDeliverNew needs a NATS server with JetStream (NXL_TEST_NATS_URL).
func TestDeliverNew(t *testing.T) {
	t.Parallel()

	url := os.Getenv("NXL_TEST_NATS_URL")
	if url == "" {
		t.Skip("NXL_TEST_NATS_URL not set")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	bus, err := eventbus.Connect(ctx, url, "eventbus-test")
	require.NoError(t, err)

	defer bus.Close()

	run := strconv.FormatInt(time.Now().UnixNano(), 36)
	typ := "test.deliver_new_" + run + ".v1"
	require.NoError(t, bus.Publish(ctx, typ, run+"-1", []byte(`{"n":1}`)))

	var all, fresh atomic.Int32

	durableAll, durableNew := "test-all-"+run, "test-new-"+run

	stopAll, err := bus.Consume(ctx, durableAll, []string{typ}, func(context.Context, eventbus.Event) error {
		all.Add(1)

		return nil
	}, nil)
	require.NoError(t, err)

	defer stopAll()

	stopNew, err := bus.Consume(ctx, durableNew, []string{typ}, func(context.Context, eventbus.Event) error {
		fresh.Add(1)

		return nil
	}, nil, eventbus.DeliverNew())
	require.NoError(t, err)

	require.NoError(t, bus.Publish(ctx, typ, run+"-2", []byte(`{"n":2}`)))
	require.Eventually(t, func() bool { return all.Load() == 2 && fresh.Load() == 1 }, 5*time.Second, 50*time.Millisecond)

	// Re-attaching an existing DeliverNew durable keeps its policy instead of failing.
	stopNew()

	stopAgain, err := bus.Consume(ctx, durableNew, []string{typ}, func(context.Context, eventbus.Event) error { return nil }, nil,
		eventbus.DeliverNew())
	require.NoError(t, err)
	stopAgain()

	require.NoError(t, bus.DeleteConsumer(ctx, durableNew))
	require.NoError(t, bus.DeleteConsumer(ctx, durableNew), "deleting a missing durable is not an error")
	require.NoError(t, bus.DeleteConsumer(ctx, durableAll))
}

// TestPull needs a NATS server with JetStream (NXL_TEST_NATS_URL).
func TestPull(t *testing.T) {
	t.Parallel()

	url := os.Getenv("NXL_TEST_NATS_URL")
	if url == "" {
		t.Skip("NXL_TEST_NATS_URL not set")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	bus, err := eventbus.Connect(ctx, url, "eventbus-test")
	require.NoError(t, err)

	defer bus.Close()

	run := strconv.FormatInt(time.Now().UnixNano(), 36)
	typ, durable := "test.pull_"+run+".v1", "test-pull-"+run

	p, err := bus.Pull(ctx, durable, []string{typ}, eventbus.MaxAckPending(1), eventbus.MaxDeliver(3),
		eventbus.AckWait(time.Minute))
	require.NoError(t, err)

	defer func() { require.NoError(t, bus.DeleteConsumer(context.WithoutCancel(ctx), durable)) }()

	_, err = p.Next(ctx, 300*time.Millisecond)
	require.ErrorIs(t, err, eventbus.ErrNoEvent)

	require.NoError(t, bus.Publish(ctx, typ, run+"-1", []byte(`{"n":1}`)))

	m, err := p.Next(ctx, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, run+"-1", m.ID)
	require.Equal(t, uint64(1), m.Delivered)
	require.NotZero(t, m.Seq)
	require.NoError(t, m.InProgress())
	require.NoError(t, m.NakWithDelay(10*time.Millisecond))

	again, err := p.Next(ctx, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, uint64(2), again.Delivered, "a nak counts as a delivery")
	require.NoError(t, again.Ack())
}
