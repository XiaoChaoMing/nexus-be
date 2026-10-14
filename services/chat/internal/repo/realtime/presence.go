package realtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

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
func (s *Store) SetFollow(ctx context.Context, channelID, userID string, at time.Time) error {
	if err := s.rdb.HSet(ctx, _followKey+channelID, userID, at.Unix()).Err(); err != nil {
		return fmt.Errorf("realtime - SetFollow: %w", err)
	}

	return nil
}

// RemoveFollow -.
func (s *Store) RemoveFollow(ctx context.Context, channelID, userID string) error {
	if err := s.rdb.HDel(ctx, _followKey+channelID, userID).Err(); err != nil {
		return fmt.Errorf("realtime - RemoveFollow: %w", err)
	}

	return nil
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
