package ws

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

const (
	_actorInbox = 1024
	// How long an out-of-order event waits for the missing ones before the hole is read from Redis.
	_gapWait     = 150 * time.Millisecond
	_redisWait   = 2 * time.Second
	_listenRetry = time.Second
	_window      = time.Second
)

type (
	subReq struct {
		cl       *client
		lastSeq  *int64
		snapshot any
	}
	unsubReq struct{ cl *client }
	evMsg    struct{ ev entity.Event }
	gapMsg   struct{}
	tickMsg  struct{}
	// localMsg is a connection-local frame (no seq) for every subscriber of the topic.
	localMsg struct{ frame []byte }
)

// member is one subscriber: it gets the events with seq > after.
type member struct {
	after  int64
	bucket *bucket
}

type actor struct {
	h     *Hub
	topic string
	chat  bool
	in    chan any
	quit  chan struct{}

	// Owned by the run goroutine only.
	subs     map[*client]*member
	last     int64 // highest seq delivered in order; -1 until known
	pending  map[int64]entity.Event
	gapArmed bool
	listen   func()

	winStart  time.Time
	winCount  int
	sampling  bool
	tickArmed bool
}

func newActor(h *Hub, topic string) *actor {
	t, _ := entity.ParseTopic(topic) //nolint:errcheck // topic was parsed by the caller

	return &actor{
		h: h, topic: topic, chat: t.Kind == entity.TopicChat,
		in: make(chan any, _actorInbox), quit: make(chan struct{}),
		subs: map[*client]*member{}, last: -1, pending: map[int64]entity.Event{},
	}
}

func (a *actor) stop() { close(a.quit) }

// post delivers a control message; it gives up when the actor is stopping.
func (a *actor) post(msg any) {
	select {
	case a.in <- msg:
	case <-a.quit:
	}
}

func (a *actor) subscribe(cl *client, lastSeq *int64, snapshot any) {
	a.post(subReq{cl: cl, lastSeq: lastSeq, snapshot: snapshot})
}

func (a *actor) unsubscribe(cl *client) { a.post(unsubReq{cl: cl}) }

func (a *actor) run() {
	a.startListening()

	defer func() {
		if a.listen != nil {
			a.listen()
		}
	}()

	a.initSeq()

	for {
		select {
		case <-a.quit:
			return
		case msg := <-a.in:
			a.handle(msg)
		}
	}
}

// startListening subscribes to the topic's NATS subject. NATS events arriving while the inbox is full
// are dropped: the seq gap that follows makes the actor read them back from Redis.
func (a *actor) startListening() {
	for {
		stop, err := a.h.gw.Listen(a.topic, func(ev entity.Event) {
			select {
			case a.in <- evMsg{ev: ev}:
			default:
			}
		})
		if err == nil {
			a.listen = stop

			return
		}

		a.h.l.Warn("ws - listen %s: %v", a.topic, err)

		select {
		case <-a.quit:
			return
		case <-time.After(_listenRetry):
		}
	}
}

// initSeq reads the current seq after the NATS subscription exists, so no later event is missed.
func (a *actor) initSeq() {
	ctx, cancel := context.WithTimeout(a.h.ctx, _redisWait)
	defer cancel()

	if seq, err := a.h.gw.Seq(ctx, a.topic); err == nil {
		a.last = seq
	} else {
		a.h.l.Warn("ws - seq of %s: %v", a.topic, err)
	}
}

func (a *actor) handle(msg any) {
	switch m := msg.(type) {
	case subReq:
		a.onSubscribe(m)
	case unsubReq:
		delete(a.subs, m.cl)
	case evMsg:
		a.onEvent(m.ev)
	case gapMsg:
		a.onGap()
	case tickMsg:
		a.tickArmed = false
		a.window(time.Now(), false)
	case localMsg:
		sent := 0

		for cl := range a.subs {
			if cl.enqueue(m.frame) {
				sent++
			}
		}

		a.h.m.Delivered(sent)
	}
}

// onSubscribe sends `subscribed`, then the events the client missed since lastSeq or `resync` when the
// buffer cannot cover them (NXL-00 §10.3).
func (a *actor) onSubscribe(r subReq) {
	m := &member{after: max(a.last, 0)}
	if a.chat && a.h.o.SamplingCap > 0 {
		m.bucket = newBucket(float64(a.h.o.SamplingCap), float64(a.h.o.SamplingCap))
	}

	var replay [][]byte

	if r.lastSeq != nil && *r.lastSeq != m.after {
		replay = a.resume(m, *r.lastSeq)
	}

	a.subs[r.cl] = m

	r.cl.enqueue(encode(subscribedFrame{Op: "subscribed", Topic: a.topic, Seq: m.after, Snapshot: r.snapshot}))

	if a.sampling {
		r.cl.enqueue(encodeLocal(a.topic, entity.EventSampling, samplingData{Active: true}, time.Now()))
	}

	for _, f := range replay {
		if !r.cl.enqueue(f) {
			return
		}
	}

	a.h.m.Delivered(len(replay))
}

func (a *actor) resume(m *member, lastSeq int64) [][]byte {
	resync := [][]byte{encode(topicFrame{Op: "resync", Topic: a.topic})}

	ctx, cancel := context.WithTimeout(a.h.ctx, _redisWait)
	defer cancel()

	if lastSeq > m.after {
		// The client is ahead of this instance (events still in flight here) unless the counter was reset.
		if cur, err := a.h.gw.Seq(ctx, a.topic); err == nil && lastSeq <= cur {
			m.after = lastSeq

			return nil
		}

		a.h.m.Resync()

		return resync
	}

	if lastSeq < 0 || m.after-lastSeq > int64(a.h.o.ResumeMaxGap) {
		a.h.m.Resync()

		return resync
	}

	evs, ok, err := a.h.gw.Since(ctx, a.topic, lastSeq, a.h.o.ResumeMaxGap)
	if err != nil || !ok {
		a.h.m.Resync()

		return resync
	}

	out := make([][]byte, 0, len(evs))

	for _, ev := range evs {
		if ev.Seq <= m.after {
			out = append(out, encodeEvent(ev))
		}
	}

	return out
}

func (a *actor) onEvent(ev entity.Event) {
	switch {
	case a.last < 0:
		a.deliver(ev) // seq was unknown: the first event sets the baseline
	case ev.Seq <= a.last:
		// already delivered (NATS redelivery or filled from Redis)
	case ev.Seq == a.last+1:
		a.deliver(ev)
		a.flushPending()
	default:
		a.pending[ev.Seq] = ev
		if !a.gapArmed {
			a.gapArmed = true

			time.AfterFunc(_gapWait, func() { a.post(gapMsg{}) })
		}
	}
}

func (a *actor) flushPending() {
	for {
		ev, ok := a.pending[a.last+1]
		if !ok {
			return
		}

		delete(a.pending, ev.Seq)
		a.deliver(ev)
	}
}

// onGap reads the missing events from Redis; when they are gone too, subscribers resync.
func (a *actor) onGap() {
	a.gapArmed = false

	if len(a.pending) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(a.h.ctx, _redisWait)
	defer cancel()

	evs, ok, err := a.h.gw.Since(ctx, a.topic, a.last, a.h.o.ResumeMaxGap)
	if err == nil && ok {
		for _, ev := range evs {
			if ev.Seq == a.last+1 {
				delete(a.pending, ev.Seq)
				a.deliver(ev)
			}
		}

		a.flushPending()
	}

	if len(a.pending) == 0 {
		return
	}

	keys := make([]int64, 0, len(a.pending))
	for k := range a.pending {
		keys = append(keys, k)
	}

	a.last = slices.Max(keys)
	clear(a.pending)

	frame := encode(topicFrame{Op: "resync", Topic: a.topic})
	for cl, m := range a.subs {
		m.after = a.last

		cl.enqueue(frame)
	}

	a.h.m.Resync()
}

func (a *actor) deliver(ev entity.Event) {
	a.last = ev.Seq
	now := time.Now()
	a.h.m.FanoutLatency(now.Sub(ev.TS))

	msg := a.sampled(ev, now)
	frame := encodeEvent(ev)
	sent := 0

	for cl, m := range a.subs {
		if ev.Seq <= m.after || (msg != nil && m.bucket != nil && !priority(msg, cl) && !m.bucket.allow(now)) {
			continue // already replayed to it, or sampled out (NXL-B BR22)
		}

		if cl.enqueue(frame) {
			sent++
		}
	}

	a.h.m.Delivered(sent)
}

// sampled counts chat messages and, while sampling is on, returns the decoded message so that
// subscribers can be filtered; nil means every subscriber gets the event.
func (a *actor) sampled(ev entity.Event, now time.Time) *entity.Message {
	if !a.chat || ev.Type != entity.EventMessage {
		return nil
	}

	a.window(now, true)

	if !a.sampling {
		return nil
	}

	msg := &entity.Message{}
	if err := json.Unmarshal(ev.Data, msg); err != nil {
		return nil
	}

	return msg
}

// window counts messages per second: above the threshold sampling starts at once; it stops when a
// window ends without being busy (a timer re-checks when messages stop arriving).
func (a *actor) window(now time.Time, message bool) {
	if a.h.o.SamplingThreshold <= 0 {
		return
	}

	if elapsed := now.Sub(a.winStart); elapsed >= _window {
		busy := a.winCount > a.h.o.SamplingThreshold && elapsed < 2*_window
		a.setSampling(busy)
		a.winStart, a.winCount = now, 0
	}

	if message {
		a.winCount++
		if a.winCount > a.h.o.SamplingThreshold {
			a.setSampling(true)
		}
	}

	if a.sampling && !a.tickArmed {
		a.tickArmed = true

		time.AfterFunc(_window, func() { a.post(tickMsg{}) })
	}
}

func (a *actor) setSampling(on bool) {
	if on == a.sampling {
		return
	}

	a.sampling = on

	frame := encodeLocal(a.topic, entity.EventSampling, samplingData{Active: on}, time.Now())
	for cl := range a.subs {
		cl.enqueue(frame)
	}
}

// priority messages are never sampled out: the reader's own, those mentioning them, and those of the
// streamer and moderators (NXL-B BR22). System events are not messages and always pass.
func priority(m *entity.Message, cl *client) bool {
	userID := cl.principal().UserID
	if userID != "" && (m.User.ID == userID || m.MentionsUser(userID)) {
		return true
	}

	return slices.Contains(m.User.Badges, entity.BadgeStreamer) || slices.Contains(m.User.Badges, entity.BadgeModerator)
}
