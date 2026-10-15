package ws_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/controller/ws"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/stretchr/testify/require"
)

const (
	streamID = "0199d4a2-0000-7000-8000-0000000000aa"
	userA    = "0199d4a2-0000-7000-8000-000000000001"
	userB    = "0199d4a2-0000-7000-8000-000000000002"
	chatT    = "chat:" + streamID
)

var errBadToken = errors.New("bad token")

// bus stands in for Redis (seq + buffer) and NATS (listeners) of every instance.
type bus struct {
	mu        sync.Mutex
	seq       map[string]int64
	buf       map[string][]entity.Event
	listeners map[string]map[int]func(entity.Event)
	nextID    int
}

func newBus() *bus {
	return &bus{seq: map[string]int64{}, buf: map[string][]entity.Event{}, listeners: map[string]map[int]func(entity.Event){}}
}

// publish numbers and buffers an event and hands it to the listeners (in order unless deliver=false).
func (b *bus) publish(topic, typ string, data any, deliver bool) entity.Event {
	raw, _ := json.Marshal(data) //nolint:errcheck // test data

	b.mu.Lock()
	b.seq[topic]++
	ev := entity.Event{Topic: topic, Seq: b.seq[topic], Type: typ, Data: raw, TS: time.Now().UTC()}
	b.buf[topic] = append(b.buf[topic], ev)
	b.mu.Unlock()

	if deliver {
		b.emit(ev)
	}

	return ev
}

func (b *bus) emit(ev entity.Event) {
	b.mu.Lock()

	fns := make([]func(entity.Event), 0, len(b.listeners[ev.Topic]))
	for _, fn := range b.listeners[ev.Topic] {
		fns = append(fns, fn)
	}
	b.mu.Unlock()

	for _, fn := range fns {
		fn(ev)
	}
}

func (b *bus) listening(topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.listeners[topic]) > 0
}

type fakeGateway struct {
	b *bus
}

func (fakeGateway) Authorize(_ context.Context, token *string, anonID string) (entity.Principal, error) {
	if token == nil {
		return entity.Principal{AnonID: anonID}, nil
	}

	if *token == "locked" {
		return entity.Principal{}, entity.ErrAccountLocked
	}

	id, ok := strings.CutPrefix(*token, "good-")
	if !ok {
		return entity.Principal{}, errBadToken
	}

	return entity.Principal{UserID: id, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (fakeGateway) ConnOpened(context.Context, entity.Principal) error { return nil }
func (fakeGateway) ConnAlive(context.Context, entity.Principal)        {}
func (fakeGateway) ConnClosed(context.Context, entity.Principal)       {}
func (fakeGateway) Leave(entity.Principal, entity.Topic)               {}

func (fakeGateway) SidebarCounts(_ context.Context, userID string) ([]entity.SidebarCount, error) {
	return []entity.SidebarCount{{ChannelID: userID, StreamID: streamID, Count: 7}}, nil
}

func (fakeGateway) Join(_ context.Context, p entity.Principal, t entity.Topic) (any, error) {
	if (t.Kind == entity.TopicUser || t.Kind == entity.TopicSidebar) && t.ID != p.UserID {
		return nil, entity.ErrForbidden
	}

	return map[string]string{"room": "OPEN"}, nil
}

func (g fakeGateway) Seq(_ context.Context, topic string) (int64, error) {
	g.b.mu.Lock()
	defer g.b.mu.Unlock()

	return g.b.seq[topic], nil
}

func (g fakeGateway) Since(_ context.Context, topic string, after int64, limit int) ([]entity.Event, bool, error) {
	g.b.mu.Lock()
	defer g.b.mu.Unlock()

	out := []entity.Event{}

	for _, ev := range g.b.buf[topic] {
		if ev.Seq > after && len(out) < limit {
			out = append(out, ev)
		}
	}

	return out, len(out) > 0 && out[0].Seq == after+1, nil
}

func (g fakeGateway) Listen(topic string, fn func(entity.Event)) (func(), error) {
	g.b.mu.Lock()
	defer g.b.mu.Unlock()

	if g.b.listeners[topic] == nil {
		g.b.listeners[topic] = map[int]func(entity.Event){}
	}

	g.b.nextID++
	id := g.b.nextID
	g.b.listeners[topic][id] = fn

	return func() {
		g.b.mu.Lock()
		defer g.b.mu.Unlock()

		delete(g.b.listeners[topic], id)
	}, nil
}

type fakeChat struct{ usecase.Chat }

func (fakeChat) Send(_ context.Context, p entity.Principal, _ string, in entity.SendInput) (entity.SendResult, error) {
	switch {
	case p.Guest():
		return entity.SendResult{}, entity.ErrUnauthenticated
	case in.Body == "slow":
		return entity.SendResult{}, entity.Reject(entity.ErrSlowMode, "retryAfterSec", "4")
	}

	return entity.SendResult{Message: entity.Message{ID: "m1"}}, nil
}

type nopMetrics struct{}

func (nopMetrics) ConnOpened()                 {}
func (nopMetrics) ConnClosed()                 {}
func (nopMetrics) Subscriptions(int)           {}
func (nopMetrics) Delivered(int)               {}
func (nopMetrics) FanoutLatency(time.Duration) {}
func (nopMetrics) Resync()                     {}

type server struct {
	url string
	hub *ws.Hub
	bus *bus
}

func start(t *testing.T, mutate func(*ws.Options)) server {
	t.Helper()

	o := ws.Options{
		AuthTimeout: 300 * time.Millisecond, Heartbeat: 25 * time.Second, IdleTimeout: 5 * time.Second,
		MaxSubscriptions: 3, SendQueue: 64, ResumeMaxGap: 50, AllowedOrigins: []string{"http://localhost:5173"},
	}
	if mutate != nil {
		mutate(&o)
	}

	b := newBus()
	hub := ws.NewHub(fakeGateway{b: b}, fakeChat{}, o, nopMetrics{}, logger.New("error"))
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	hub.Register(app)

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = app.Listener(ln) }() //nolint:errcheck // returns when the test shuts the app down

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		hub.Shutdown(ctx)
		_ = app.Shutdown() //nolint:errcheck // test teardown
	})

	return server{url: "ws://" + ln.Addr().String() + "/v1/ws", hub: hub, bus: b}
}

type conn struct {
	t *testing.T
	c *websocket.Conn
}

func dial(t *testing.T, s server, header http.Header) conn {
	t.Helper()

	c, resp, err := websocket.DefaultDialer.Dial(s.url, header)
	require.NoError(t, err)

	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() // test
	}

	t.Cleanup(func() { _ = c.Close() }) // test

	return conn{t: t, c: c}
}

func (c conn) send(v any) {
	c.t.Helper()
	require.NoError(c.t, c.c.WriteJSON(v))
}

// next reads the next frame as a map.
func (c conn) next() map[string]any {
	c.t.Helper()
	require.NoError(c.t, c.c.SetReadDeadline(time.Now().Add(3*time.Second)))

	var m map[string]any
	require.NoError(c.t, c.c.ReadJSON(&m))

	return m
}

// closed waits for the server's close frame and returns its code.
func (c conn) closed() int {
	c.t.Helper()
	require.NoError(c.t, c.c.SetReadDeadline(time.Now().Add(3*time.Second)))

	for {
		_, _, err := c.c.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			require.ErrorAs(c.t, err, &ce)

			return ce.Code
		}
	}
}

func (c conn) auth(token any) map[string]any {
	c.t.Helper()
	c.send(map[string]any{"op": "auth", "token": token, "anonId": "0199d4a2-0000-7000-8000-00000000a0a0"})

	return c.next()
}

func waitListening(t *testing.T, b *bus, topic string) {
	t.Helper()
	require.Eventually(t, func() bool { return b.listening(topic) }, 2*time.Second, 5*time.Millisecond)
}

func TestAuthRequiredFirst(t *testing.T) {
	t.Parallel()

	s := start(t, nil)

	require.Equal(t, ws.CloseAuthTimeout, dial(t, s, nil).closed(), "no auth in time")

	c := dial(t, s, nil)
	c.send(map[string]any{"op": "ping"})
	require.Equal(t, ws.CloseUnauthorized, c.closed(), "first frame is not auth")

	c = dial(t, s, nil)
	c.send(map[string]any{"op": "auth", "token": "nope"})
	require.Equal(t, ws.CloseUnauthorized, c.closed(), "invalid token")
}

func TestOriginCheck(t *testing.T) {
	t.Parallel()

	s := start(t, nil)

	_, resp, err := websocket.DefaultDialer.Dial(s.url, http.Header{"Origin": {"http://evil.example"}})
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	_ = resp.Body.Close() // test

	c := dial(t, s, http.Header{"Origin": {"http://localhost:5173"}})
	require.Equal(t, "ready", c.auth(nil)["op"])
}

func TestGuestReadsButCannotSend(t *testing.T) {
	t.Parallel()

	s := start(t, nil)
	c := dial(t, s, nil)

	ready := c.auth(nil)
	require.Equal(t, "ready", ready["op"])
	require.Nil(t, ready["userId"])
	require.InDelta(t, 25, ready["heartbeatSec"], 0)

	c.send(map[string]any{"op": "subscribe", "topic": chatT})
	sub := c.next()
	require.Equal(t, "subscribed", sub["op"])
	require.InDelta(t, 0, sub["seq"], 0)
	require.Equal(t, map[string]any{"room": "OPEN"}, sub["snapshot"])

	waitListening(t, s.bus, chatT)
	s.bus.publish(chatT, "message", map[string]string{"body": "hi"}, true)

	ev := c.next()
	require.Equal(t, "event", ev["op"])
	require.Equal(t, chatT, ev["topic"])
	require.InDelta(t, 1, ev["seq"], 0)
	require.Equal(t, "message", ev["type"])

	c.send(map[string]any{"op": "send", "topic": chatT, "type": "message", "data": map[string]string{"body": "hi"}, "clientMsgId": "k1"})
	errFrame := c.next()
	require.Equal(t, "error", errFrame["op"])
	require.Equal(t, "UNAUTHORIZED", errFrame["code"])
	require.Equal(t, "k1", errFrame["ref"])

	c.send(map[string]any{"op": "ping"})
	require.Equal(t, "pong", c.next()["op"])
}

func TestSendAckAndErrors(t *testing.T) {
	t.Parallel()

	s := start(t, nil)
	c := dial(t, s, nil)
	require.Equal(t, userA, c.auth("good-" + userA)["userId"])

	c.send(map[string]any{"op": "send", "topic": chatT, "type": "message", "data": map[string]string{"body": "hello"}, "clientMsgId": "k1"})
	ack := c.next()
	require.Equal(t, "event", ack["op"])
	require.Equal(t, "ack", ack["type"])
	require.NotContains(t, ack, "seq", "ack is connection-local")
	require.Equal(t, map[string]any{"clientMsgId": "k1", "messageId": "m1"}, ack["data"])

	c.send(map[string]any{"op": "send", "topic": chatT, "type": "message", "data": map[string]string{"body": "slow"}, "clientMsgId": "k2"})
	e := c.next()
	require.Equal(t, "CHAT_SLOW_MODE", e["code"])
	require.Equal(t, map[string]any{"retryAfterSec": "4"}, e["details"])

	c.send(map[string]any{"op": "send", "topic": "stream:" + streamID, "type": "message", "data": map[string]string{"body": "x"}, "clientMsgId": "k3"})
	require.Equal(t, "VALIDATION_FAILED", c.next()["code"])

	c.send(map[string]any{"op": "subscribe", "topic": "user:" + userB})
	e = c.next()
	require.Equal(t, "FORBIDDEN", e["code"])

	c.send(map[string]any{"op": "subscribe", "topic": "chat:nope"})
	require.Equal(t, "VALIDATION_FAILED", c.next()["code"])

	c.send(map[string]any{"op": "auth", "token": "good-" + userA})
	require.Equal(t, "ready", c.next()["op"], "a refreshed token for the same user")

	c.send(map[string]any{"op": "auth", "token": "good-" + userB})
	require.Equal(t, ws.CloseUnauthorized, c.closed(), "another identity needs a new connection")
}

func TestSubscriptionLimit(t *testing.T) {
	t.Parallel()

	s := start(t, func(o *ws.Options) { o.MaxSubscriptions = 1 })
	c := dial(t, s, nil)
	c.auth(nil)

	c.send(map[string]any{"op": "subscribe", "topic": chatT})
	require.Equal(t, "subscribed", c.next()["op"])

	c.send(map[string]any{"op": "subscribe", "topic": "stream:" + streamID})
	e := c.next()
	require.Equal(t, "RATE_LIMITED", e["code"])
	require.Equal(t, map[string]any{"limit": "1"}, e["details"])

	c.send(map[string]any{"op": "unsubscribe", "topic": chatT})
	c.send(map[string]any{"op": "subscribe", "topic": "stream:" + streamID})
	require.Equal(t, "subscribed", c.next()["op"])
}

func TestResumeAndResync(t *testing.T) {
	t.Parallel()

	s := start(t, nil)

	for range 3 {
		s.bus.publish(chatT, "message", map[string]string{"body": "x"}, true)
	}

	c := dial(t, s, nil)
	c.auth(nil)

	c.send(map[string]any{"op": "subscribe", "topic": chatT, "lastSeq": 1})
	sub := c.next()
	require.Equal(t, "subscribed", sub["op"])
	require.InDelta(t, 3, sub["seq"], 0)
	require.InDelta(t, 2, c.next()["seq"], 0)
	require.InDelta(t, 3, c.next()["seq"], 0)

	waitListening(t, s.bus, chatT)
	s.bus.publish(chatT, "message", map[string]string{"body": "live"}, true)
	require.InDelta(t, 4, c.next()["seq"], 0, "live events follow the replay")

	// A second client that was further behind than the buffer allows must reload.
	s2 := start(t, func(o *ws.Options) { o.ResumeMaxGap = 2 })
	for range 5 {
		s2.bus.publish(chatT, "message", map[string]string{"body": "x"}, true)
	}

	c2 := dial(t, s2, nil)
	c2.auth(nil)
	c2.send(map[string]any{"op": "subscribe", "topic": chatT, "lastSeq": 1})
	require.Equal(t, "subscribed", c2.next()["op"])
	require.Equal(t, map[string]any{"op": "resync", "topic": chatT}, c2.next())
}

func TestEventsAreDeliveredInSeqOrder(t *testing.T) {
	t.Parallel()

	s := start(t, nil)
	c := dial(t, s, nil)
	c.auth(nil)
	c.send(map[string]any{"op": "subscribe", "topic": chatT})
	require.Equal(t, "subscribed", c.next()["op"])
	waitListening(t, s.bus, chatT)

	// Two instances publish concurrently: NATS hands seq 2 over before seq 1, and seq 3 is lost.
	e1 := s.bus.publish(chatT, "message", map[string]string{"body": "1"}, false)
	e2 := s.bus.publish(chatT, "message", map[string]string{"body": "2"}, false)
	s.bus.publish(chatT, "message", map[string]string{"body": "3"}, false)
	e4 := s.bus.publish(chatT, "message", map[string]string{"body": "4"}, false)

	s.bus.emit(e2)
	s.bus.emit(e1)
	s.bus.emit(e4)

	for want := 1; want <= 4; want++ {
		require.InDelta(t, want, c.next()["seq"], 0, "seq 3 is read back from the buffer")
	}
}

func TestTwoClientsOneTopic(t *testing.T) {
	t.Parallel()

	s := start(t, nil)
	a, b := dial(t, s, nil), dial(t, s, nil)
	a.auth("good-" + userA)
	b.auth(nil)

	streamT := "stream:" + streamID

	for _, c := range []conn{a, b} {
		c.send(map[string]any{"op": "subscribe", "topic": streamT})
		require.Equal(t, "subscribed", c.next()["op"])
	}

	waitListening(t, s.bus, streamT)
	s.bus.publish(streamT, "viewer_count", map[string]int{"count": 2}, true)

	for _, c := range []conn{a, b} {
		ev := c.next()
		require.Equal(t, "viewer_count", ev["type"])
		require.Equal(t, map[string]any{"count": float64(2)}, ev["data"])
	}
}

func TestShutdownClosesWith1012(t *testing.T) {
	t.Parallel()

	s := start(t, nil)
	c := dial(t, s, nil)
	c.auth(nil)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		s.hub.Shutdown(ctx)
	}()

	require.Equal(t, ws.CloseServiceRestart, c.closed())
	require.Eventually(t, func() bool { return s.hub.Connections() == 0 }, 2*time.Second, 10*time.Millisecond)
}

func TestSidebarBatch(t *testing.T) {
	t.Parallel()

	s := start(t, nil)
	c := dial(t, s, nil)
	c.auth("good-" + userA)

	c.send(map[string]any{"op": "subscribe", "topic": "sidebar:" + userB})
	require.Equal(t, "FORBIDDEN", c.next()["code"], "only the user's own sidebar")

	c.send(map[string]any{"op": "subscribe", "topic": "sidebar:" + userA})
	require.Equal(t, "subscribed", c.next()["op"])

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go s.hub.RunSidebarBatch(ctx, 20*time.Millisecond)

	ev := c.next()
	require.Equal(t, "viewer_count_batch", ev["type"])
	require.NotContains(t, ev, "seq", "local to the socket")
	require.Equal(t, map[string]any{"items": []any{map[string]any{"channelId": userA, "streamId": streamID, "count": float64(7)}}}, ev["data"])
}

func TestLockedAccountCloses4003(t *testing.T) {
	t.Parallel()

	s := start(t, nil)

	c := dial(t, s, nil)
	c.send(map[string]any{"op": "auth", "token": "locked"})
	require.Equal(t, ws.CloseAccountLocked, c.closed(), "a locked account cannot reconnect")

	a, b, other := dial(t, s, nil), dial(t, s, nil), dial(t, s, nil)
	a.auth("good-u1")
	b.auth("good-u1")
	other.auth("good-u2")

	require.Equal(t, 2, s.hub.CloseUser("u1", ws.CloseAccountLocked, "account_locked"))
	require.Equal(t, ws.CloseAccountLocked, a.closed())
	require.Equal(t, ws.CloseAccountLocked, b.closed())

	other.send(map[string]any{"op": "ping"})
	require.Equal(t, "pong", other.next()["op"], "other users stay connected")
}
