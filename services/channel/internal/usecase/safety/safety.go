// Package safety applies safety-svc's channel actions and serves the snapshot other services backfill
// their channel and follow copies from.
package safety

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

const _maxSnapshot = 500

// UseCase implements usecase.Safety and usecase.Snapshot.
type UseCase struct {
	status repo.SafetyRepo
	snap   repo.SnapshotRepo
	l      logger.Interface
}

var (
	_ usecase.Safety   = (*UseCase)(nil)
	_ usecase.Snapshot = (*UseCase)(nil)
)

// New -.
func New(status repo.SafetyRepo, snap repo.SnapshotRepo, l logger.Interface) *UseCase {
	return &UseCase{status: status, snap: snap, l: l}
}

// ChannelActioned suspends or restores the channel; nothing to change (same state, unknown channel) is acked.
func (uc *UseCase) ChannelActioned(ctx context.Context, eventID string, e events.ChannelActioned) error {
	changed, err := uc.status.SetStatus(ctx, eventID, entity.StatusChange{
		ChannelID: e.ChannelID, Suspend: e.Action == events.ActionSuspend, Reason: e.Reason, ActionID: e.ActionID,
	})
	if err != nil {
		return fmt.Errorf("SafetyUseCase - ChannelActioned: %w", err)
	}

	if changed {
		uc.l.Info("audit=channel_%s channel=%s action=%s admin=%s", strings.ToLower(e.Action), e.ChannelID, e.ActionID, e.AdminID)
	} else {
		uc.l.Warn("channel - %s of %s (action %s) changed nothing", e.Action, e.ChannelID, e.ActionID)
	}

	return nil
}

// Channels -.
func (uc *UseCase) Channels(ctx context.Context, after string, limit int) ([]entity.ChannelCopy, string, error) {
	if after != "" && uuid.Validate(after) != nil {
		return nil, "", entity.ErrInvalidCursor
	}

	limit = snapshotSize(limit)

	items, err := uc.snap.Channels(ctx, after, limit)
	if err != nil {
		return nil, "", fmt.Errorf("SafetyUseCase - Channels: %w", err)
	}

	next := ""
	if len(items) == limit {
		next = items[limit-1].ID
	}

	return items, next, nil
}

// Follows -. The cursor is "<userId>:<channelId>".
func (uc *UseCase) Follows(ctx context.Context, after string, limit int) ([]entity.FollowCopy, string, error) {
	var pos *entity.FollowCopy

	if after != "" {
		user, channel, ok := strings.Cut(after, ":")
		if !ok || uuid.Validate(user) != nil || uuid.Validate(channel) != nil {
			return nil, "", entity.ErrInvalidCursor
		}

		pos = &entity.FollowCopy{UserID: user, ChannelID: channel}
	}

	limit = snapshotSize(limit)

	items, err := uc.snap.Follows(ctx, pos, limit)
	if err != nil {
		return nil, "", fmt.Errorf("SafetyUseCase - Follows: %w", err)
	}

	next := ""
	if len(items) == limit {
		next = items[limit-1].UserID + ":" + items[limit-1].ChannelID
	}

	return items, next, nil
}

func snapshotSize(limit int) int {
	if limit <= 0 || limit > _maxSnapshot {
		return _maxSnapshot
	}

	return limit
}
