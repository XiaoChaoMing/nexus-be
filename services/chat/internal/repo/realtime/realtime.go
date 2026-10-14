// Package realtime implements chat-svc's Redis state (TSD §5.6): topic sequence numbers and resume
// buffers, send guards (de-duplication, rate limits, slow mode), follow/sub copies, connections and
// viewer counts. Multi-key updates are Lua scripts so that concurrent instances stay consistent.
package realtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	goredis "github.com/redis/go-redis/v9"
)

// Key prefixes. viewers:{streamId} is read by stream-svc.
const (
	_seqKey      = "rt:seq:"
	_bufKey      = "rt:buf:"
	_dedupeKey   = "chat:dedupe:"
	_lastKey     = "chat:last:"
	_slowKey     = "chat:slow:"
	_followKey   = "follow:"
	_subKey      = "sub:"
	_connKey     = "ws:conn:"
	_viewersZ    = "viewers:z:"
	_viewersN    = "viewers:"
	_viewersPeak = "viewers:peak:"
	_viewersLead = "viewers:leader"
	_viewersSet  = "viewers:active"
	_statusKey   = "stream:status:"

	_seqTTL     = 24 * time.Hour
	_dedupeTTL  = 10 * time.Minute
	_connTTL    = 90 * time.Second
	_viewersTTL = time.Minute
	_peakTTL    = 24 * time.Hour
	_statusTTL  = 24 * time.Hour
)

// Options -.
type Options struct {
	// ResumeBuffer is how many events a topic keeps (WS_RESUME_BUFFER), ResumeTTL for how long.
	ResumeBuffer    int
	ResumeTTL       time.Duration
	DuplicateWindow time.Duration
}

// Store implements the Redis contracts of repo.
type Store struct {
	rdb     *redis.Client
	o       Options
	now     func() time.Time
	scripts scripts
}

type scripts struct {
	appendEvent, take, addConn, lead, refresh *goredis.Script
}

var (
	_ repo.SeqStore      = (*Store)(nil)
	_ repo.SendGuard     = (*Store)(nil)
	_ repo.AudienceStore = (*Store)(nil)
	_ repo.PresenceStore = (*Store)(nil)
)

// New -.
func New(rdb *redis.Client, o Options) *Store {
	return &Store{rdb: rdb, o: o, now: time.Now, scripts: scripts{
		appendEvent: goredis.NewScript(_appendLua),
		take:        goredis.NewScript(_takeLua),
		addConn:     goredis.NewScript(_addConnLua),
		lead:        goredis.NewScript(_leadLua),
		refresh:     goredis.NewScript(_refreshLua),
	}}
}

// ---- sequence and resume buffer ------------------------------------------------------------------

// _appendLua numbers the event and appends it with the stream entry id "<seq>-1", so a resume reads
// from "<after+1>-0". The buffer is recreated if a reset counter would go below its last id.
const _appendLua = `
local seq = redis.call('INCR', KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
local ok = redis.pcall('XADD', KEYS[2], 'MAXLEN', '~', ARGV[5], seq .. '-1', 't', ARGV[1], 'd', ARGV[2], 'ts', ARGV[3])
if type(ok) == 'table' and ok.err then
  redis.call('DEL', KEYS[2])
  redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[5], seq .. '-1', 't', ARGV[1], 'd', ARGV[2], 'ts', ARGV[3])
end
redis.call('PEXPIRE', KEYS[2], ARGV[6])
return seq
`

// Append -.
func (s *Store) Append(ctx context.Context, ev entity.Event) (entity.Event, error) {
	seq, err := s.scripts.appendEvent.Run(ctx, s.rdb, []string{_seqKey + ev.Topic, _bufKey + ev.Topic},
		ev.Type, string(ev.Data), ev.TS.UnixMilli(), _seqTTL.Milliseconds(), s.o.ResumeBuffer, s.o.ResumeTTL.Milliseconds()).Int64()
	if err != nil {
		return entity.Event{}, fmt.Errorf("realtime - Append: %w", err)
	}

	ev.Seq = seq

	return ev, nil
}

// Seq -.
func (s *Store) Seq(ctx context.Context, topic string) (int64, error) {
	n, err := s.rdb.Get(ctx, _seqKey+topic).Int64()
	if errors.Is(err, goredis.Nil) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("realtime - Seq: %w", err)
	}

	return n, nil
}

// Since -.
func (s *Store) Since(ctx context.Context, topic string, after int64, limit int) (events []entity.Event, complete bool, err error) {
	pipe := s.rdb.Pipeline()
	cur := pipe.Get(ctx, _seqKey+topic)
	rng := pipe.XRangeN(ctx, _bufKey+topic, strconv.FormatInt(after+1, 10)+"-0", "+", int64(limit))

	if _, err = pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return nil, false, fmt.Errorf("realtime - Since: %w", err)
	}

	current, err := cur.Int64()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return nil, false, fmt.Errorf("realtime - Since - seq: %w", err)
	}

	if current <= after {
		// Nothing missed, or the counter was reset (current < after): then the client must resync.
		return []entity.Event{}, current == after, nil
	}

	events, complete = s.decodeRange(topic, after, rng.Val())

	return events, complete, nil
}

// decodeRange keeps the entries only when they start right after `after` and are not older than the
// resume TTL; otherwise the range cannot be replayed.
func (s *Store) decodeRange(topic string, after int64, msgs []goredis.XMessage) ([]entity.Event, bool) {
	out := make([]entity.Event, 0, len(msgs))
	oldest := s.now().Add(-s.o.ResumeTTL).UnixMilli()

	for _, m := range msgs {
		ev, ok := decodeEntry(topic, m)
		if !ok || (len(out) == 0 && (ev.Seq != after+1 || ev.TS.UnixMilli() < oldest)) {
			return nil, false
		}

		out = append(out, ev)
	}

	return out, len(out) > 0
}

func decodeEntry(topic string, m goredis.XMessage) (entity.Event, bool) {
	seqPart, _, _ := strings.Cut(m.ID, "-")

	seq, err := strconv.ParseInt(seqPart, 10, 64)
	if err != nil {
		return entity.Event{}, false
	}

	typ, okType := m.Values["t"].(string)
	data, okData := m.Values["d"].(string)
	tsStr, okTS := m.Values["ts"].(string)

	if !okType || !okData || !okTS {
		return entity.Event{}, false
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return entity.Event{}, false
	}

	return entity.Event{Topic: topic, Seq: seq, Type: typ, Data: []byte(data), TS: time.UnixMilli(ts).UTC()}, true
}

// ---- send guards ---------------------------------------------------------------------------------

// AcceptedMessage -.
func (s *Store) AcceptedMessage(ctx context.Context, userID, clientMsgID string) (messageID string, found bool, err error) {
	id, err := s.rdb.Get(ctx, _dedupeKey+userID+":"+clientMsgID).Result()
	if errors.Is(err, goredis.Nil) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("realtime - AcceptedMessage: %w", err)
	}

	return id, true, nil
}

// RememberMessage -.
func (s *Store) RememberMessage(ctx context.Context, userID, streamID, clientMsgID, messageID, bodyHash string) error {
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, _dedupeKey+userID+":"+clientMsgID, messageID, _dedupeTTL)

	if s.o.DuplicateWindow > 0 {
		pipe.Set(ctx, _lastKey+userID+":"+streamID, bodyHash, s.o.DuplicateWindow)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("realtime - RememberMessage: %w", err)
	}

	return nil
}

// LastBodyHash -.
func (s *Store) LastBodyHash(ctx context.Context, userID, streamID string) (string, error) {
	h, err := s.rdb.Get(ctx, _lastKey+userID+":"+streamID).Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("realtime - LastBodyHash: %w", err)
	}

	return h, nil
}

// _takeLua is a token bucket refilled continuously (capacity per window), on the Redis clock so that
// every instance agrees. It returns {allowed, wait ms}.
const _takeLua = `
local cap = tonumber(ARGV[1])
local win = tonumber(ARGV[2])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local rate = cap / win
local b = redis.call('HMGET', KEYS[1], 'tk', 'ts')
local tokens = tonumber(b[1])
local ts = tonumber(b[2])
if tokens == nil or ts == nil then
  tokens = cap
  ts = now
end
tokens = math.min(cap, tokens + math.max(0, now - ts) * rate)
local allowed = 0
local wait = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  wait = math.ceil((1 - tokens) / rate)
end
redis.call('HSET', KEYS[1], 'tk', tostring(tokens), 'ts', tostring(now))
redis.call('PEXPIRE', KEYS[1], win)
return {allowed, wait}
`

// Take -.
func (s *Store) Take(ctx context.Context, key string, capacity int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	res, err := s.scripts.take.Run(ctx, s.rdb, []string{key}, capacity, window.Milliseconds()).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("realtime - Take: %w", err)
	}

	if len(res) != 2 { //nolint:mnd // {allowed, wait}
		return false, 0, fmt.Errorf("realtime - Take: %w", errUnexpectedReply)
	}

	return res[0] == 1, time.Duration(res[1]) * time.Millisecond, nil
}

var errUnexpectedReply = errors.New("unexpected script reply")

// StartSlow -.
func (s *Store) StartSlow(ctx context.Context, streamID, userID string, d time.Duration) (bool, time.Duration, error) {
	key := _slowKey + streamID + ":" + userID

	ok, err := s.rdb.SetNX(ctx, key, 1, d).Result()
	if err != nil {
		return false, 0, fmt.Errorf("realtime - StartSlow: %w", err)
	}

	if ok {
		return true, 0, nil
	}

	left, err := s.rdb.PTTL(ctx, key).Result()
	if err != nil {
		return false, 0, fmt.Errorf("realtime - StartSlow - ttl: %w", err)
	}

	return false, max(left, time.Millisecond), nil
}

// SlowLeft -.
func (s *Store) SlowLeft(ctx context.Context, streamID, userID string) (time.Duration, error) {
	left, err := s.rdb.PTTL(ctx, _slowKey+streamID+":"+userID).Result()
	if err != nil {
		return 0, fmt.Errorf("realtime - SlowLeft: %w", err)
	}

	return max(left, 0), nil
}
