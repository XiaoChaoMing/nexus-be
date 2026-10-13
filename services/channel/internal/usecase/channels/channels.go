// Package channels implements channel creation, live state and the public channel page.
package channels

import (
	"context"
	"fmt"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
)

// UseCase implements usecase.Directory and usecase.Sync.
type UseCase struct {
	repo repo.ChannelRepo
	l    logger.Interface
}

var (
	_ usecase.Directory = (*UseCase)(nil)
	_ usecase.Sync      = (*UseCase)(nil)
)

// New -.
func New(r repo.ChannelRepo, l logger.Interface) *UseCase {
	return &UseCase{repo: r, l: l}
}

// ByHandle -.
func (uc *UseCase) ByHandle(ctx context.Context, handle string) (entity.Channel, error) {
	if !entity.ValidHandle(handle) {
		return entity.Channel{}, entity.ErrNotFound
	}

	ch, err := uc.repo.ByHandle(ctx, handle)
	if err != nil {
		return entity.Channel{}, fmt.Errorf("ChannelUseCase - ByHandle: %w", err)
	}

	if ch.Suspended() {
		return entity.Channel{}, entity.ErrSuspended
	}

	return ch, nil
}

// CreatorApproved -.
func (uc *UseCase) CreatorApproved(ctx context.Context, eventID string, in usecase.CreatorApproved) error {
	base := entity.BaseHandle(in.Username)

	ch, created, err := uc.repo.CreateForOwner(ctx, eventID, repo.NewChannel{
		OwnerID:     in.UserID,
		BaseHandle:  base,
		DisplayName: entity.DisplayName(in.DisplayName, in.Username),
	})
	if err != nil {
		return fmt.Errorf("ChannelUseCase - CreatorApproved: %w", err)
	}

	if !created {
		uc.l.Info("channel - creator %s already has a channel or the event was processed: skipped", in.UserID)

		return nil
	}

	if ch.Handle != base {
		uc.l.Info("channel - handle %q taken, channel %s created as %q", base, ch.ID, ch.Handle)
	}

	uc.l.Info("channel - created %s (%s) for creator %s", ch.ID, ch.Handle, in.UserID)

	return nil
}

// StreamStarted -.
func (uc *UseCase) StreamStarted(ctx context.Context, eventID, channelID, streamID string) error {
	if err := uc.repo.SetLive(ctx, eventID, channelID, streamID); err != nil {
		return fmt.Errorf("ChannelUseCase - StreamStarted: %w", err)
	}

	return nil
}

// StreamEnded -.
func (uc *UseCase) StreamEnded(ctx context.Context, eventID, channelID, streamID string) error {
	if err := uc.repo.SetOffline(ctx, eventID, channelID, streamID); err != nil {
		return fmt.Errorf("ChannelUseCase - StreamEnded: %w", err)
	}

	return nil
}
