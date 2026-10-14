package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/gateway"
)

const (
	_maxFrameBytes = 8 * 1024
	_writeWait     = 10 * time.Second
	_opTimeout     = 5 * time.Second
	// Frames per second a client may send (every op), with a burst; beyond it the connection is closed.
	_frameRate  = 20
	_frameBurst = 40
)

type closeReq struct {
	code   int
	reason string
}

type client struct {
	h    *Hub
	conn *websocket.Conn
	id   string
	ip   string

	// ctx lives as long as the connection: ops started from it stop when the client goes away.
	ctx    context.Context
	cancel context.CancelFunc
	send   chan []byte
	ctrl   chan closeReq
	frames *bucket

	mu     sync.Mutex
	p      entity.Principal
	authed bool
	subs   map[string]entity.Topic
	expiry *time.Timer
}

func newClient(h *Hub, conn *websocket.Conn, ip string) *client {
	ctx, cancel := context.WithCancel(h.ctx)
	id := uuid.NewString()

	return &client{
		h: h, conn: conn, id: id, ip: ip, ctx: ctx, cancel: cancel,
		send:   make(chan []byte, h.o.SendQueue),
		ctrl:   make(chan closeReq, 1),
		frames: newBucket(_frameRate, _frameBurst),
		p:      entity.Principal{ConnID: id, IP: ip},
		subs:   map[string]entity.Topic{},
	}
}

func (cl *client) run() {
	writerDone := make(chan struct{})

	go func() {
		defer close(writerDone)

		cl.writeLoop()
	}()

	cl.readLoop()
	cl.cleanup()
	cl.cancel()
	<-writerDone
	cl.h.remove(cl)
}

// closeWith asks the writer to send a close frame and end the connection; the first request wins.
func (cl *client) closeWith(code int, reason string) {
	select {
	case cl.ctrl <- closeReq{code: code, reason: reason}:
	default:
	}
}

// closeImmediately is for connections refused before their writer starts.
func (cl *client) closeImmediately(code int, reason string) {
	_ = cl.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(_writeWait)) //nolint:errcheck // peer may be gone
}

// enqueue never blocks: a client that cannot keep up is disconnected (NXL-00 §10.4, 4029).
func (cl *client) enqueue(frame []byte) bool {
	select {
	case cl.send <- frame:
		return true
	default:
		cl.closeWith(CloseTooMany, "slow consumer")

		return false
	}
}

func (cl *client) writeLoop() {
	for {
		select {
		case req := <-cl.ctrl:
			cl.writeClose(req)

			return
		case frame := <-cl.send:
			if err := cl.conn.SetWriteDeadline(time.Now().Add(_writeWait)); err != nil {
				return
			}

			if err := cl.conn.WriteMessage(websocket.TextMessage, frame); err != nil {
				cl.unblockReader()

				return
			}
		case <-cl.ctx.Done():
			select {
			case req := <-cl.ctrl:
				cl.writeClose(req)
			default:
			}

			return
		}
	}
}

func (cl *client) writeClose(req closeReq) {
	_ = cl.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(req.code, req.reason), time.Now().Add(_writeWait)) //nolint:errcheck // peer may be gone
	cl.unblockReader()
}

// unblockReader makes the pending read fail now, so the handler ends.
func (cl *client) unblockReader() {
	_ = cl.conn.SetReadDeadline(time.Now()) //nolint:errcheck // the connection may already be closed
}

func (cl *client) readLoop() {
	cl.conn.SetReadLimit(_maxFrameBytes)

	if err := cl.conn.SetReadDeadline(time.Now().Add(cl.h.o.AuthTimeout)); err != nil {
		return
	}

	for {
		_, data, err := cl.conn.ReadMessage()
		if err != nil {
			if !cl.isAuthed() && isTimeout(err) {
				cl.closeWith(CloseAuthTimeout, "auth timeout")
			}

			return
		}

		if err = cl.conn.SetReadDeadline(time.Now().Add(cl.h.o.IdleTimeout)); err != nil {
			return
		}

		if !cl.frames.allow(time.Now()) {
			cl.closeWith(CloseTooMany, "too many messages")

			return
		}

		cl.dispatch(data)
	}
}

func isTimeout(err error) bool {
	var ne net.Error

	return errors.As(err, &ne) && ne.Timeout()
}

func (cl *client) isAuthed() bool {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	return cl.authed
}

func (cl *client) principal() entity.Principal {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	return cl.p
}

func (cl *client) dispatch(data []byte) {
	var f inFrame
	if err := json.Unmarshal(data, &f); err != nil {
		cl.enqueue(encodeError(entity.CodeValidationFailed, "invalid frame", "", nil))

		return
	}

	if f.Op == opAuth {
		cl.onAuth(f)

		return
	}

	// The first frame must be auth (NXL-00 §10.1).
	if !cl.isAuthed() {
		cl.closeWith(CloseUnauthorized, "auth required")

		return
	}

	switch f.Op {
	case opSubscribe:
		cl.onSubscribe(f)
	case opUnsubscribe:
		cl.onUnsubscribe(f)
	case opPing:
		cl.enqueue([]byte(`{"op":"pong"}`))
		cl.h.gw.ConnAlive(cl.ctx, cl.principal())
	case opSend:
		cl.onSend(f)
	default:
		cl.enqueue(encodeError(entity.CodeValidationFailed, "unknown op", "", map[string]string{"op": entity.DetailEnum}))
	}
}

// onAuth authenticates the connection or extends it with a refreshed token. Switching to another
// identity (guest ↔ account, another account) needs a new connection: the server closes with 4001.
func (cl *client) onAuth(f inFrame) {
	ctx, cancel := context.WithTimeout(cl.ctx, _opTimeout)
	defer cancel()

	p, err := cl.h.gw.Authorize(ctx, f.Token, f.AnonID)
	if err != nil {
		cl.closeWith(CloseUnauthorized, "invalid token")

		return
	}

	p.ConnID, p.IP = cl.id, cl.ip

	if !cl.identify(ctx, p) {
		return
	}

	cl.armExpiry(p.ExpiresAt)

	var userID *string
	if !p.Guest() {
		userID = &p.UserID
	}

	cl.enqueue(encode(readyFrame{Op: "ready", ConnID: cl.id, UserID: userID, HeartbeatSec: int(cl.h.o.Heartbeat.Seconds())}))
}

// identify stores the principal of the first auth, or the new expiry of a refreshed token; it reports
// false when the connection is being closed.
func (cl *client) identify(ctx context.Context, p entity.Principal) bool {
	cl.mu.Lock()
	authed, current := cl.authed, cl.p
	cl.mu.Unlock()

	if authed {
		if current.UserID != p.UserID {
			cl.closeWith(CloseUnauthorized, "identity changed, reconnect")

			return false
		}

		cl.mu.Lock()
		cl.p.ExpiresAt = p.ExpiresAt
		cl.mu.Unlock()

		return true
	}

	err := cl.h.gw.ConnOpened(ctx, p)
	if errors.Is(err, entity.ErrTooManyConnections) {
		cl.closeWith(CloseTooMany, "too many connections")

		return false
	}

	if err != nil {
		cl.h.l.Warn("ws - conn limit unavailable: %v", err)
	}

	cl.mu.Lock()
	cl.p, cl.authed = p, true
	cl.mu.Unlock()

	return true
}

// armExpiry closes the connection 60 s after the token expires unless a new auth arrives (NXL-00 §10.1).
func (cl *client) armExpiry(exp time.Time) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if cl.expiry != nil {
		cl.expiry.Stop()
		cl.expiry = nil
	}

	if exp.IsZero() {
		return
	}

	cl.expiry = time.AfterFunc(time.Until(exp)+gateway.TokenGrace, func() {
		cl.closeWith(CloseUnauthorized, "token expired")
	})
}

func (cl *client) onSubscribe(f inFrame) {
	topic, err := entity.ParseTopic(f.Topic)
	if err != nil {
		cl.enqueue(encodeError(entity.CodeValidationFailed, "invalid topic", f.Topic, map[string]string{"topic": entity.DetailFormat}))

		return
	}

	key := topic.String()

	cl.mu.Lock()
	_, already := cl.subs[key]
	full := !already && len(cl.subs) >= cl.h.o.MaxSubscriptions
	cl.mu.Unlock()

	if full {
		cl.enqueue(encodeError(entity.CodeRateLimited, "too many subscriptions", key,
			map[string]string{"limit": strconv.Itoa(cl.h.o.MaxSubscriptions)}))

		return
	}

	if already {
		cl.leave(topic) // re-subscribing resumes from the new lastSeq
	}

	ctx, cancel := context.WithTimeout(cl.ctx, _opTimeout)
	defer cancel()

	snap, err := cl.h.gw.Join(ctx, cl.principal(), topic)
	if err != nil {
		cl.enqueueErr(err, key)

		return
	}

	cl.mu.Lock()
	cl.subs[key] = topic
	cl.mu.Unlock()

	cl.h.m.Subscriptions(1)
	cl.h.acquire(key).subscribe(cl, f.LastSeq, snap)
}

func (cl *client) onUnsubscribe(f inFrame) {
	topic, err := entity.ParseTopic(f.Topic)
	if err != nil {
		return
	}

	cl.mu.Lock()
	_, ok := cl.subs[topic.String()]
	cl.mu.Unlock()

	if ok {
		cl.leave(topic)
	}
}

func (cl *client) leave(topic entity.Topic) {
	key := topic.String()

	cl.mu.Lock()
	delete(cl.subs, key)
	cl.mu.Unlock()

	if a := cl.h.lookup(key); a != nil {
		a.unsubscribe(cl)
	}

	cl.h.release(key)
	cl.h.gw.Leave(cl.principal(), topic)
	cl.h.m.Subscriptions(-1)
}

type sendData struct {
	Body string `json:"body"`
}

func (cl *client) onSend(f inFrame) {
	topic, err := entity.ParseTopic(f.Topic)
	if err != nil || topic.Kind != entity.TopicChat {
		cl.enqueue(encodeError(entity.CodeValidationFailed, "only chat topics accept messages", f.ClientMsgID,
			map[string]string{"topic": entity.DetailEnum}))

		return
	}

	if f.Type != entity.EventMessage {
		cl.enqueue(encodeError(entity.CodeValidationFailed, "unknown type", f.ClientMsgID, map[string]string{"type": entity.DetailEnum}))

		return
	}

	var d sendData
	if len(f.Data) > 0 {
		if err = json.Unmarshal(f.Data, &d); err != nil {
			cl.enqueue(encodeError(entity.CodeValidationFailed, "invalid data", f.ClientMsgID, map[string]string{"body": entity.DetailFormat}))

			return
		}
	}

	ctx, cancel := context.WithTimeout(cl.ctx, _opTimeout)
	defer cancel()

	res, err := cl.h.chat.Send(ctx, cl.principal(), topic.ID, entity.SendInput{ClientMsgID: f.ClientMsgID, Body: d.Body})
	if err != nil {
		cl.enqueueErr(err, f.ClientMsgID)

		return
	}

	cl.enqueue(encodeLocal(topic.String(), entity.EventAck, ackData{ClientMsgID: f.ClientMsgID, MessageID: res.Message.ID}, time.Now()))
}

// enqueueErr sends the error frame of a use-case error; unexpected errors are logged, not shown.
func (cl *client) enqueueErr(err error, ref string) {
	code := entity.CodeOf(err)
	msg := "internal error"

	if code == entity.CodeInternal {
		cl.h.l.Error("ws - %v", err)
	} else {
		var re *entity.RejectError
		if errors.As(err, &re) {
			msg = re.Err.Error()
		} else {
			msg = rootMessage(err)
		}
	}

	cl.enqueue(encodeError(code, msg, ref, entity.DetailsOf(err)))
}

// rootMessage is the sentinel's text, without the layer path of wrapped errors.
func rootMessage(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err.Error()
		}

		err = next
	}
}

// cleanup undoes every subscription and the connection registration.
func (cl *client) cleanup() {
	cl.mu.Lock()

	topics := make([]entity.Topic, 0, len(cl.subs))
	for _, t := range cl.subs {
		topics = append(topics, t)
	}

	if cl.expiry != nil {
		cl.expiry.Stop()
	}

	authed, p := cl.authed, cl.p
	cl.mu.Unlock()

	for _, t := range topics {
		cl.leave(t)
	}

	if authed {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(cl.ctx), _opTimeout)
		defer cancel()

		cl.h.gw.ConnClosed(ctx, p)
	}
}
