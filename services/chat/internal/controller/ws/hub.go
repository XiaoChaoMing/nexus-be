// Package ws is the realtime gateway: GET /v1/ws (NXL-00 §10, TSD §5.1).
//
// Each connection has one reader (this handler's goroutine, which also runs the client's ops in order)
// and one writer fed by a bounded queue; a full queue closes the connection with 4029 instead of
// slowing anybody else down. Each topic with listeners on this instance has one actor goroutine that
// receives the topic's events from NATS, puts them back in seq order (filling holes from the Redis
// buffer) and copies them to the subscribers' queues. Subscribing goes through the same actor, so a
// resume replay can never interleave with live events.
package ws

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

const (
	_localIP       = "nxl-ip"
	_ioBufferBytes = 4096
)

// Options are the gateway limits (config WS and Chat).
type Options struct {
	AuthTimeout       time.Duration
	Heartbeat         time.Duration
	IdleTimeout       time.Duration
	MaxSubscriptions  int
	SendQueue         int
	ResumeMaxGap      int
	AllowedOrigins    []string
	SamplingThreshold int
	SamplingCap       int
}

// Metrics receives the gateway counters (metrics.Metrics).
type Metrics interface {
	ConnOpened()
	ConnClosed()
	Subscriptions(delta int)
	Delivered(n int)
	FanoutLatency(d time.Duration)
	Resync()
}

// Hub owns the connections and topic actors of this instance.
type Hub struct {
	gw   usecase.Gateway
	chat usecase.Chat
	o    Options
	l    logger.Interface
	m    Metrics

	// ctx bounds the actors' Redis calls; canceled by Shutdown.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	actors   map[string]*actorRef
	clients  map[*client]struct{}
	closing  bool
	handlers sync.WaitGroup
}

type actorRef struct {
	a    *actor
	refs int
}

// NewHub -.
func NewHub(gw usecase.Gateway, chat usecase.Chat, o Options, m Metrics, l logger.Interface) *Hub {
	ctx, cancel := context.WithCancel(context.Background())

	return &Hub{
		gw: gw, chat: chat, o: o, l: l, m: m, ctx: ctx, cancel: cancel,
		actors: map[string]*actorRef{}, clients: map[*client]struct{}{},
	}
}

// Register adds GET /v1/ws.
func (h *Hub) Register(app fiber.Router) {
	app.Get("/v1/ws", h.upgrade, websocket.New(h.serve, websocket.Config{
		ReadBufferSize:  _ioBufferBytes,
		WriteBufferSize: _ioBufferBytes,
	}))
}

// upgrade refuses non-WebSocket requests, foreign browser origins and new connections during shutdown.
func (h *Hub) upgrade(ctx *fiber.Ctx) error {
	if !websocket.IsWebSocketUpgrade(ctx) {
		return httpError(ctx, http.StatusUpgradeRequired, "VALIDATION_FAILED", "websocket upgrade required")
	}

	// Browsers always send Origin; scripts and load tests usually do not and are not a CSRF vector.
	if origin := ctx.Get(fiber.HeaderOrigin); origin != "" && !slices.Contains(h.o.AllowedOrigins, origin) {
		return httpError(ctx, http.StatusForbidden, "FORBIDDEN", "origin not allowed")
	}

	h.mu.Lock()
	closing := h.closing
	h.mu.Unlock()

	if closing {
		return httpError(ctx, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "restarting")
	}

	ctx.Locals(_localIP, clientIP(ctx))

	return ctx.Next()
}

func httpError(ctx *fiber.Ctx, status int, code, message string) error {
	return ctx.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}

// clientIP is the address the edge saw: the last X-Forwarded-For entry (Nginx appends it), else the
// peer address. Earlier entries are client-supplied and not trusted. The header is a view of a pooled
// buffer and the value outlives the request (it stays with the connection), so it is copied.
func clientIP(ctx *fiber.Ctx) string {
	if xff := ctx.Get(fiber.HeaderXForwardedFor); xff != "" {
		parts := strings.Split(xff, ",")

		return strings.Clone(strings.TrimSpace(parts[len(parts)-1]))
	}

	return ctx.Context().RemoteIP().String()
}

func (h *Hub) serve(conn *websocket.Conn) {
	ip, _ := conn.Locals(_localIP).(string) //nolint:errcheck // empty when missing; only used for rate limits

	cl := newClient(h, conn, ip)
	if !h.add(cl) {
		cl.closeImmediately(CloseServiceRestart, "restarting")

		return
	}

	defer h.handlers.Done()

	h.m.ConnOpened()
	defer h.m.ConnClosed()

	cl.run()
}

func (h *Hub) add(cl *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closing {
		return false
	}

	h.clients[cl] = struct{}{}
	h.handlers.Add(1)

	return true
}

func (h *Hub) remove(cl *client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.clients, cl)
}

// acquire returns the topic's actor, starting it for the first subscriber.
func (h *Hub) acquire(topic string) *actor {
	h.mu.Lock()
	defer h.mu.Unlock()

	ref, ok := h.actors[topic]
	if !ok {
		ref = &actorRef{a: newActor(h, topic)}
		h.actors[topic] = ref

		go ref.a.run()
	}

	ref.refs++

	return ref.a
}

// lookup returns the topic's running actor, if any.
func (h *Hub) lookup(topic string) *actor {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ref, ok := h.actors[topic]; ok {
		return ref.a
	}

	return nil
}

// release stops the actor when its last subscriber leaves.
func (h *Hub) release(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ref, ok := h.actors[topic]
	if !ok {
		return
	}

	ref.refs--
	if ref.refs <= 0 {
		delete(h.actors, topic)
		ref.a.stop()
	}
}

// Shutdown closes every connection with 1012 (clients reconnect to another instance) and waits for
// their handlers, at most until ctx expires.
func (h *Hub) Shutdown(ctx context.Context) {
	h.mu.Lock()
	h.closing = true

	clients := make([]*client, 0, len(h.clients))
	for cl := range h.clients {
		clients = append(clients, cl)
	}
	h.mu.Unlock()

	for _, cl := range clients {
		cl.closeWith(CloseServiceRestart, "service restart")
	}

	done := make(chan struct{})

	go func() {
		h.handlers.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		h.l.Warn("ws - shutdown: %d connections still open", len(clients))
	}

	h.cancel()
}

// Connections is the number of open connections (tests, logs).
func (h *Hub) Connections() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return len(h.clients)
}

type sidebarBatchData struct {
	Items []entity.SidebarCount `json:"items"`
}

// RunSidebarBatch sends viewer_count_batch to the sidebar: subscribers of this instance every interval until ctx
// is canceled (each instance serves its own sockets, like sampling).
func (h *Hub) RunSidebarBatch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.sidebarBatch(ctx)
		}
	}
}

func (h *Hub) sidebarBatch(ctx context.Context) {
	for _, a := range h.actorsOf(entity.TopicSidebar) {
		topic, err := entity.ParseTopic(a.topic)
		if err != nil {
			continue
		}

		rctx, cancel := context.WithTimeout(ctx, _redisWait)
		items, err := h.gw.SidebarCounts(rctx, topic.ID)

		cancel()

		if err != nil {
			h.l.Warn("ws - sidebar counts of %s: %v", topic.ID, err)

			continue
		}

		if len(items) > 0 {
			a.post(localMsg{frame: encodeLocal(a.topic, entity.EventViewerCountBatch, sidebarBatchData{Items: items}, time.Now())})
		}
	}
}

// actorsOf returns the running actors of one topic kind.
func (h *Hub) actorsOf(kind entity.TopicKind) []*actor {
	h.mu.Lock()
	defer h.mu.Unlock()

	prefix := string(kind) + ":"
	out := []*actor{}

	for topic, ref := range h.actors {
		if strings.HasPrefix(topic, prefix) {
			out = append(out, ref.a)
		}
	}

	return out
}
