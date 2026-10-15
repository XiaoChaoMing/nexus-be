// Package throttle keeps the live cooldowns and the daily e-mail counts of notification-svc in Redis.
package throttle

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
	goredis "github.com/redis/go-redis/v9"
)

// _dayTTL keeps a daily counter past the end of the day in any time zone.
const _dayTTL = 48 * time.Hour

// Throttle implements repo.Throttle.
type Throttle struct {
	rdb *redis.Client
}

var _ repo.Throttle = (*Throttle)(nil)

// New -.
func New(rdb *redis.Client) *Throttle { return &Throttle{rdb: rdb} }

func liveKey(userID, channelID string) string { return "notify:live:" + userID + ":" + channelID }

func liveEmailKey(userID, channelID string) string {
	return "notify:live-email:" + userID + ":" + channelID
}

func dayKey(userID, day string) string { return "notify:email-day:" + userID + ":" + day }

// LiveCooldowns -.
func (t *Throttle) LiveCooldowns(ctx context.Context, channelID string, userIDs []string) (inApp, email map[string]string,
	err error,
) {
	inApp, email = map[string]string{}, map[string]string{}
	if len(userIDs) == 0 {
		return inApp, email, nil
	}

	keys := make([]string, 0, 2*len(userIDs)) //nolint:mnd // two keys per user
	for _, id := range userIDs {
		keys = append(keys, liveKey(id, channelID), liveEmailKey(id, channelID))
	}

	vals, err := t.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("Throttle - LiveCooldowns: %w", err)
	}

	for i, id := range userIDs {
		if s, ok := vals[2*i].(string); ok {
			inApp[id] = s
		}

		if s, ok := vals[2*i+1].(string); ok {
			email[id] = s
		}
	}

	return inApp, email, nil
}

// SetLiveCooldowns -.
func (t *Throttle) SetLiveCooldowns(ctx context.Context, channelID, eventID string, inApp, email []string,
	inAppTTL, emailTTL time.Duration,
) error {
	_, err := t.rdb.Pipelined(ctx, func(p goredis.Pipeliner) error {
		for _, id := range inApp {
			p.Set(ctx, liveKey(id, channelID), eventID, inAppTTL)
		}

		for _, id := range email {
			p.Set(ctx, liveEmailKey(id, channelID), eventID, emailTTL)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Throttle - SetLiveCooldowns: %w", err)
	}

	return nil
}

// EmailsToday -.
func (t *Throttle) EmailsToday(ctx context.Context, days map[string]string) (map[string]int, error) {
	out := make(map[string]int, len(days))
	if len(days) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(days))
	keys := make([]string, 0, len(days))

	for id, day := range days {
		ids = append(ids, id)
		keys = append(keys, dayKey(id, day))
	}

	vals, err := t.rdb.MGet(ctx, keys...).Result()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return nil, fmt.Errorf("Throttle - EmailsToday: %w", err)
	}

	for i, id := range ids {
		if s, ok := vals[i].(string); ok {
			if n, err := strconv.Atoi(s); err == nil {
				out[id] = n
			}
		}
	}

	return out, nil
}

// CountEmails -.
func (t *Throttle) CountEmails(ctx context.Context, days map[string]string) error {
	if len(days) == 0 {
		return nil
	}

	_, err := t.rdb.Pipelined(ctx, func(p goredis.Pipeliner) error {
		for id, day := range days {
			p.Incr(ctx, dayKey(id, day))
			p.Expire(ctx, dayKey(id, day), _dayTTL)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Throttle - CountEmails: %w", err)
	}

	return nil
}
