package channels

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

// Paging of GET /v1/me/following (NXL-00 §6).
const (
	DefaultLimit = 24
	MaxLimit     = 50
)

// Follow -.
func (uc *UseCase) Follow(ctx context.Context, userID, channelID string) (entity.Follow, error) {
	ch, err := uc.activeChannel(ctx, channelID)
	if err != nil {
		return entity.Follow{}, err
	}

	if ch.OwnerID == userID {
		return entity.Follow{}, entity.ErrCannotFollowSelf
	}

	f, created, err := uc.r.Follows.Follow(ctx, userID, channelID)
	if err != nil {
		return entity.Follow{}, fmt.Errorf("ChannelUseCase - Follow: %w", err)
	}

	if created {
		uc.l.Info("channel - user %s follows %s", userID, channelID)
	}

	return f, nil
}

// SetNotifyLevel -.
func (uc *UseCase) SetNotifyLevel(ctx context.Context, userID, channelID string, l entity.NotifyLevel) (entity.Follow, error) {
	if _, err := uc.r.Channels.ByID(ctx, channelID); err != nil {
		return entity.Follow{}, fmt.Errorf("ChannelUseCase - SetNotifyLevel: %w", err)
	}

	f, err := uc.r.Follows.SetNotifyLevel(ctx, userID, channelID, l)
	if err != nil {
		return entity.Follow{}, fmt.Errorf("ChannelUseCase - SetNotifyLevel: %w", err)
	}

	return f, nil
}

// Unfollow -.
func (uc *UseCase) Unfollow(ctx context.Context, userID, channelID string) error {
	if err := uc.r.Follows.Unfollow(ctx, userID, channelID); err != nil {
		return fmt.Errorf("ChannelUseCase - Unfollow: %w", err)
	}

	return nil
}

// Following lists live channels by viewers (no paging, at most FollowingLiveMax), or every / offline
// followed channel with a keyset cursor.
func (uc *UseCase) Following(ctx context.Context, userID string, f entity.FollowingFilter, cursor string, limit int) (usecase.FollowingPage, error) {
	if limit <= 0 || limit > MaxLimit {
		limit = DefaultLimit
	}

	if f == entity.FollowingLive {
		live, err := uc.liveFollowed(ctx, userID)
		if err != nil {
			return usecase.FollowingPage{}, err
		}

		return usecase.FollowingPage{Items: live}, nil
	}

	var after *repo.FollowCursor

	if cursor != "" {
		c, err := decodeFollowCursor(cursor)
		if err != nil {
			return usecase.FollowingPage{}, err
		}

		after = &c
	}

	items, err := uc.r.Follows.ListFollowed(ctx, userID, f == entity.FollowingOffline, after, limit+1)
	if err != nil {
		return usecase.FollowingPage{}, fmt.Errorf("ChannelUseCase - Following: %w", err)
	}

	page := usecase.FollowingPage{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeFollowCursor(page.Items[limit-1])
	}

	if err := uc.attachLive(ctx, page.Items); err != nil {
		return usecase.FollowingPage{}, err
	}

	return page, nil
}

// Sidebar returns every followed live channel (up to limit) by viewers, then offline ones to fill limit.
func (uc *UseCase) Sidebar(ctx context.Context, userID string, limit int) (entity.Sidebar, error) {
	live, err := uc.liveFollowed(ctx, userID)
	if err != nil {
		return entity.Sidebar{}, err
	}

	if len(live) > limit {
		live = live[:limit]
	}

	sb := entity.Sidebar{Live: live, Offline: []entity.FollowedChannel{}}

	if rest := limit - len(live); rest > 0 {
		if sb.Offline, err = uc.r.Follows.ListFollowed(ctx, userID, true, nil, rest); err != nil {
			return entity.Sidebar{}, fmt.Errorf("ChannelUseCase - Sidebar - offline: %w", err)
		}
	}

	if sb.TotalFollowing, err = uc.r.Follows.CountByUser(ctx, userID); err != nil {
		return entity.Sidebar{}, fmt.Errorf("ChannelUseCase - Sidebar - count: %w", err)
	}

	return sb, nil
}

func (uc *UseCase) liveFollowed(ctx context.Context, userID string) ([]entity.FollowedChannel, error) {
	items, err := uc.r.Follows.ListLive(ctx, userID, entity.FollowingLiveMax)
	if err != nil {
		return nil, fmt.Errorf("ChannelUseCase - followed live: %w", err)
	}

	if err := uc.attachLive(ctx, items); err != nil {
		return nil, err
	}

	sort.SliceStable(items, func(i, j int) bool {
		vi, vj := items[i].Viewers(), items[j].Viewers()
		if vi != vj {
			return vi > vj
		}

		return startedAfter(items[i], items[j])
	})

	return items, nil
}

func startedAfter(a, b entity.FollowedChannel) bool {
	if a.CurrentStream == nil || b.CurrentStream == nil {
		return a.CurrentStream != nil
	}

	return a.CurrentStream.StartedAt.After(b.CurrentStream.StartedAt)
}

// attachLive fills CurrentStream of the live channels among items.
func (uc *UseCase) attachLive(ctx context.Context, items []entity.FollowedChannel) error {
	ids := []string{}

	for _, it := range items {
		if it.Channel.IsLive {
			ids = append(ids, it.Channel.ID)
		}
	}

	if len(ids) == 0 {
		return nil
	}

	live, err := uc.r.Channels.LiveStreams(ctx, ids)
	if err != nil {
		return fmt.Errorf("ChannelUseCase - live streams: %w", err)
	}

	streams := make([]*entity.LiveStream, 0, len(live))

	for i := range items {
		if s, ok := live[items[i].Channel.ID]; ok {
			items[i].CurrentStream = &s
			streams = append(streams, items[i].CurrentStream)
		}
	}

	uc.enrich.Live(ctx, streams)

	return nil
}

type followCursor struct {
	IsLive     bool   `json:"l"`
	LastLiveAt int64  `json:"t"`
	ChannelID  string `json:"c"`
}

func encodeFollowCursor(fc entity.FollowedChannel) string {
	c := followCursor{IsLive: fc.Channel.IsLive, ChannelID: fc.Channel.ID}
	if fc.Channel.LastLiveAt != nil {
		c.LastLiveAt = fc.Channel.LastLiveAt.UnixMicro()
	}

	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeFollowCursor(s string) (repo.FollowCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return repo.FollowCursor{}, badCursor()
	}

	var c followCursor
	if err = json.Unmarshal(b, &c); err != nil {
		return repo.FollowCursor{}, badCursor()
	}

	if _, err = uuid.Parse(c.ChannelID); err != nil {
		return repo.FollowCursor{}, badCursor()
	}

	return repo.FollowCursor{IsLive: c.IsLive, LastLiveAt: unixMicroUTC(c.LastLiveAt), ChannelID: c.ChannelID}, nil
}

func unixMicroUTC(n int64) time.Time { return time.UnixMicro(n).UTC() }

func badCursor() error {
	return entity.Reject(entity.ErrValidation, "cursor", entity.DetailInvalid)
}
