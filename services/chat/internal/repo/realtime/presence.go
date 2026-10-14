package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	goredis "github.com/redis/go-redis/v9"
)

// ---- follow / sub copies -------------------------------------------------------------------------

// Audience -.
func (s *Store) Audience(ctx context.Context, channelID, userID string) (*time.Time, int, error) {
	pipe := s.rdb.Pipeline()
	f := pipe.HGet(ctx, _followKey+channelID, userID)
	t := pipe.HGet(ctx, _subKey+channelID, userID)

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return nil, 0, fmt.Errorf("realtime - Audience: %w", err)
	}

	var followedAt *time.Time

	if secs, err := f.Int64(); err == nil {
		at := time.Unix(secs, 0).UTC()
		followedAt = &at
	}

	tier, err := t.Int()
	if err != nil {
		tier = 0
	}

	return followedAt, tier, nil
}

// SubTiers -.
func (s *Store) SubTiers(ctx context.Context, channelID string, userIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(userIDs) == 0 {
		return out, nil
	}

	vals, err := s.rdb.HMGet(ctx, _subKey+channelID, userIDs...).Result()
	if err != nil {
		return nil, fmt.Errorf("realtime - SubTiers: %w", err)
	}

	for i, v := range vals {
		str, ok := v.(string)
		if !ok {
			continue
		}

		if tier, perr := strconv.Atoi(str); perr == nil && tier > 0 {
			out[userIDs[i]] = tier
		}
	}

	return out, nil
}

// SetFollow -.
func (s *Store) SetFollow(ctx context.Context, channelID, userID string, at time.Time, notifyLevel string) error {
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, _followKey+channelID, userID, at.Unix())
	pipe.SAdd(ctx, _followingKey+userID, channelID)
	pipe.HSet(ctx, _notifyKey+channelID, userID, notifyLevel)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("realtime - SetFollow: %w", err)
	}

	return nil
}

// RemoveFollow -.
func (s *Store) RemoveFollow(ctx context.Context, channelID, userID string) error {
	pipe := s.rdb.TxPipeline()
	pipe.HDel(ctx, _followKey+channelID, userID)
	pipe.SRem(ctx, _followingKey+userID, channelID)
	pipe.HDel(ctx, _notifyKey+channelID, userID)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("realtime - RemoveFollow: %w", err)
	}

	return nil
}

// SetNotifyLevel -.
func (s *Store) SetNotifyLevel(ctx context.Context, channelID, userID, notifyLevel string) error {
	if err := s.rdb.HSet(ctx, _notifyKey+channelID, userID, notifyLevel).Err(); err != nil {
		return fmt.Errorf("realtime - SetNotifyLevel: %w", err)
	}

	return nil
}

// Followers -.
func (s *Store) Followers(ctx context.Context, channelID string) ([]string, error) {
	ids, err := s.rdb.HKeys(ctx, _followKey+channelID).Result()
	if err != nil {
		return nil, fmt.Errorf("realtime - Followers: %w", err)
	}

	return ids, nil
}

// Following -.
func (s *Store) Following(ctx context.Context, userID string) ([]string, error) {
	ids, err := s.rdb.SMembers(ctx, _followingKey+userID).Result()
	if err != nil {
		return nil, fmt.Errorf("realtime - Following: %w", err)
	}

	return ids, nil
}

// SetSub -.
func (s *Store) SetSub(ctx context.Context, channelID, userID string, tier int) error {
	if err := s.rdb.HSet(ctx, _subKey+channelID, userID, tier).Err(); err != nil {
		return fmt.Errorf("realtime - SetSub: %w", err)
	}

	return nil
}

// RemoveSub -.
func (s *Store) RemoveSub(ctx context.Context, channelID, userID string) error {
	if err := s.rdb.HDel(ctx, _subKey+channelID, userID).Err(); err != nil {
		return fmt.Errorf("realtime - RemoveSub: %w", err)
	}

	return nil
}

// ---- connections ---------------------------------------------------------------------------------

// _addConnLua drops connections not refreshed for the TTL (a crashed instance never removes its own),
// then admits the connection unless the user is at the limit.
const _addConnLua = `
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', tonumber(ARGV[1]) - tonumber(ARGV[3]))
if redis.call('ZSCORE', KEYS[1], ARGV[2]) == false and redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[4]) then
  return 0
end
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`

// AddConn -.
func (s *Store) AddConn(ctx context.Context, userID, connID string, maxConns int) (bool, error) {
	ok, err := s.scripts.addConn.Run(ctx, s.rdb, []string{_connKey + userID},
		s.now().UnixMilli(), connID, _connTTL.Milliseconds(), maxConns).Int()
	if err != nil {
		return false, fmt.Errorf("realtime - AddConn: %w", err)
	}

	return ok == 1, nil
}

// TouchConn -.
func (s *Store) TouchConn(ctx context.Context, userID, connID string) error {
	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, _connKey+userID, goredis.Z{Score: float64(s.now().UnixMilli()), Member: connID})
	pipe.PExpire(ctx, _connKey+userID, _connTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("realtime - TouchConn: %w", err)
	}

	return nil
}

// RemoveConn -.
func (s *Store) RemoveConn(ctx context.Context, userID, connID string) error {
	if err := s.rdb.ZRem(ctx, _connKey+userID, connID).Err(); err != nil {
		return fmt.Errorf("realtime - RemoveConn: %w", err)
	}

	return nil
}

// ---- viewers -------------------------------------------------------------------------------------

// TouchViewers -.
func (s *Store) TouchViewers(ctx context.Context, streamID string, keys []string, now time.Time) error {
	if len(keys) == 0 {
		return nil
	}

	members := make([]goredis.Z, len(keys))
	for i, k := range keys {
		members[i] = goredis.Z{Score: float64(now.UnixMilli()), Member: k}
	}

	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, _viewersZ+streamID, members...)
	pipe.PExpire(ctx, _viewersZ+streamID, _viewersTTL)
	pipe.SAdd(ctx, _viewersSet, streamID)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("realtime - TouchViewers: %w", err)
	}

	return nil
}

// CountViewers -.
func (s *Store) CountViewers(ctx context.Context, streamID string, since time.Time) (int, error) {
	n, err := s.rdb.ZCount(ctx, _viewersZ+streamID, strconv.FormatInt(since.UnixMilli(), 10), "+inf").Result()
	if err != nil {
		return 0, fmt.Errorf("realtime - CountViewers: %w", err)
	}

	return int(n), nil
}

const _leadLua = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
if redis.call('SET', KEYS[1], ARGV[1], 'NX', 'PX', ARGV[2]) then
  return 1
end
return 0
`

// LeadViewers -.
func (s *Store) LeadViewers(ctx context.Context, instance string, ttl time.Duration) (bool, error) {
	ok, err := s.scripts.lead.Run(ctx, s.rdb, []string{_viewersLead}, instance, ttl.Milliseconds()).Int()
	if err != nil {
		return false, fmt.Errorf("realtime - LeadViewers: %w", err)
	}

	return ok == 1, nil
}

// WatchedStreams -.
func (s *Store) WatchedStreams(ctx context.Context) ([]string, error) {
	ids, err := s.rdb.SMembers(ctx, _viewersSet).Result()
	if err != nil {
		return nil, fmt.Errorf("realtime - WatchedStreams: %w", err)
	}

	return ids, nil
}

// _refreshLua returns {count, changed}.
const _refreshLua = `
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', '(' .. ARGV[1])
local n = redis.call('ZCARD', KEYS[1])
local old = redis.call('GET', KEYS[2])
redis.call('SET', KEYS[2], n, 'PX', ARGV[2])
local peak = tonumber(redis.call('GET', KEYS[3]) or '0')
if n > peak then
  redis.call('SET', KEYS[3], n, 'PX', ARGV[3])
end
if old == tostring(n) then
  return {n, 0}
end
return {n, 1}
`

// RefreshCount -.
func (s *Store) RefreshCount(ctx context.Context, streamID string, cutoff time.Time, ttl time.Duration) (count int, changed bool, err error) {
	res, err := s.scripts.refresh.Run(ctx, s.rdb, []string{_viewersZ + streamID, _viewersN + streamID, _viewersPeak + streamID},
		cutoff.UnixMilli(), ttl.Milliseconds(), _peakTTL.Milliseconds()).Int64Slice()
	if err != nil {
		return 0, false, fmt.Errorf("realtime - RefreshCount: %w", err)
	}

	if len(res) != 2 { //nolint:mnd // {count, changed}
		return 0, false, fmt.Errorf("realtime - RefreshCount: %w", errUnexpectedReply)
	}

	return int(res[0]), res[1] == 1, nil
}

// Peak -.
func (s *Store) Peak(ctx context.Context, streamID string) (int, error) {
	n, err := s.rdb.Get(ctx, _viewersPeak+streamID).Int()
	if errors.Is(err, goredis.Nil) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("realtime - Peak: %w", err)
	}

	return n, nil
}

// ForgetStream -.
func (s *Store) ForgetStream(ctx context.Context, streamID string) error {
	pipe := s.rdb.Pipeline()
	pipe.SRem(ctx, _viewersSet, streamID)
	pipe.Del(ctx, _viewersZ+streamID)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("realtime - ForgetStream: %w", err)
	}

	return nil
}

// SetStreamStatus -.
func (s *Store) SetStreamStatus(ctx context.Context, streamID, status string) error {
	if err := s.rdb.Set(ctx, _statusKey+streamID, status, _statusTTL).Err(); err != nil {
		return fmt.Errorf("realtime - SetStreamStatus: %w", err)
	}

	return nil
}

// StreamStatus -.
func (s *Store) StreamStatus(ctx context.Context, streamID string) (string, error) {
	st, err := s.rdb.Get(ctx, _statusKey+streamID).Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("realtime - StreamStatus: %w", err)
	}

	return st, nil
}

// Online -.
func (s *Store) Online(ctx context.Context, userIDs []string) ([]string, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	pipe := s.rdb.Pipeline()
	cmds := make([]*goredis.IntCmd, len(userIDs))

	for i, id := range userIDs {
		cmds[i] = pipe.Exists(ctx, _connKey+id)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("realtime - Online: %w", err)
	}

	out := []string{}

	for i, c := range cmds {
		if c.Val() > 0 {
			out = append(out, userIDs[i])
		}
	}

	return out, nil
}

// ViewerCounts -.
func (s *Store) ViewerCounts(ctx context.Context, streamIDs []string) (map[string]int, error) {
	out := make(map[string]int, len(streamIDs))
	if len(streamIDs) == 0 {
		return out, nil
	}

	keys := make([]string, len(streamIDs))
	for i, id := range streamIDs {
		keys[i] = _viewersN + id
	}

	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return nil, fmt.Errorf("realtime - ViewerCounts: %w", err)
	}

	for i, v := range vals {
		str, ok := v.(string)
		if !ok {
			continue
		}

		if n, perr := strconv.Atoi(str); perr == nil && n >= 0 {
			out[streamIDs[i]] = n
		}
	}

	return out, nil
}

// ---- channels on air (sidebar) -------------------------------------------------------------------

// SetLive -.
func (s *Store) SetLive(ctx context.Context, channelID string, l entity.LiveInfo) error {
	b, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("realtime - SetLive - marshal: %w", err)
	}

	if err = s.rdb.HSet(ctx, _liveKey, channelID, b).Err(); err != nil {
		return fmt.Errorf("realtime - SetLive: %w", err)
	}

	return nil
}

// _clearLiveLua removes the channel only while it still points at the ended stream (a late stream.ended must
// not clear a newer broadcast).
const _clearLiveLua = `
local cur = redis.call('HGET', KEYS[1], ARGV[1])
if cur and string.find(cur, ARGV[2], 1, true) then
  return redis.call('HDEL', KEYS[1], ARGV[1])
end
return 0`

// ClearLive -.
func (s *Store) ClearLive(ctx context.Context, channelID, streamID string) error {
	if err := s.rdb.Eval(ctx, _clearLiveLua, []string{_liveKey}, channelID, `"streamId":"`+streamID+`"`).Err(); err != nil &&
		!errors.Is(err, goredis.Nil) {
		return fmt.Errorf("realtime - ClearLive: %w", err)
	}

	return nil
}

// Live -.
func (s *Store) Live(ctx context.Context, channelIDs []string) (map[string]entity.LiveInfo, error) {
	out := map[string]entity.LiveInfo{}
	if len(channelIDs) == 0 {
		return out, nil
	}

	vals, err := s.rdb.HMGet(ctx, _liveKey, channelIDs...).Result()
	if err != nil {
		return nil, fmt.Errorf("realtime - Live: %w", err)
	}

	for i, v := range vals {
		str, ok := v.(string)
		if !ok {
			continue
		}

		var l entity.LiveInfo
		if json.Unmarshal([]byte(str), &l) == nil {
			out[channelIDs[i]] = l
		}
	}

	return out, nil
}
