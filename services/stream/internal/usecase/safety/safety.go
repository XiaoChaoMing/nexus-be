// Package safety applies admin takedowns and channel suspensions to streams and serves the stream snapshot
// safety-svc backfills its copy from.
package safety

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

const _maxSnapshot = 500

// UseCase implements usecase.Safety and usecase.Snapshot.
type UseCase struct {
	r repo.SafetyRepo
	l logger.Interface
}

var (
	_ usecase.Safety   = (*UseCase)(nil)
	_ usecase.Snapshot = (*UseCase)(nil)
)

// New -.
func New(r repo.SafetyRepo, l logger.Interface) *UseCase {
	return &UseCase{r: r, l: l}
}

// Takedown ends the stream and deletes its VOD (also one that arrives later). A stream that is not ended
// or on air (SCHEDULED, CANCELLED, unknown) is acked: safety-svc refuses those before publishing.
func (uc *UseCase) Takedown(ctx context.Context, eventID string, e events.StreamTakedown) error {
	res, err := uc.r.Takedown(ctx, eventID, e.StreamID)
	if err != nil {
		return fmt.Errorf("SafetyUseCase - Takedown: %w", err)
	}

	if !res.Found {
		uc.l.Warn("stream - takedown of %s (action %s): not ended or on air", e.StreamID, e.ActionID)

		return nil
	}

	uc.l.Info("audit=stream_takedown stream=%s action=%s admin=%s ended=%t vodsDeleted=%d",
		e.StreamID, e.ActionID, e.AdminID, res.Ended, res.VODsDeleted)

	return nil
}

// ChannelSuspended ends the channel's broadcast, cancels its scheduled streams and closes publishing.
func (uc *UseCase) ChannelSuspended(ctx context.Context, eventID string, e events.ChannelSuspended) error {
	ended, cancelled, err := uc.r.SetSuspended(ctx, eventID, e.ChannelID, true)
	if err != nil {
		return fmt.Errorf("SafetyUseCase - ChannelSuspended: %w", err)
	}

	uc.l.Info("stream - channel %s suspended: %d stream(s) ended, %d scheduled cancelled", e.ChannelID, ended, cancelled)

	return nil
}

// ChannelRestored reopens publishing; cancelled streams stay cancelled.
func (uc *UseCase) ChannelRestored(ctx context.Context, eventID string, e events.ChannelSuspended) error {
	if _, _, err := uc.r.SetSuspended(ctx, eventID, e.ChannelID, false); err != nil {
		return fmt.Errorf("SafetyUseCase - ChannelRestored: %w", err)
	}

	return nil
}

// Streams -.
func (uc *UseCase) Streams(ctx context.Context, after string, limit int) ([]entity.StreamCopy, string, error) {
	if after != "" && uuid.Validate(after) != nil {
		return nil, "", entity.ErrInvalidCursor
	}

	if limit <= 0 || limit > _maxSnapshot {
		limit = _maxSnapshot
	}

	items, err := uc.r.Streams(ctx, after, limit)
	if err != nil {
		return nil, "", fmt.Errorf("SafetyUseCase - Streams: %w", err)
	}

	next := ""
	if len(items) == limit {
		next = items[limit-1].ID
	}

	return items, next, nil
}
