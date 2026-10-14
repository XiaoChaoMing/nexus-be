package streams

import (
	"context"
	"errors"
	"fmt"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

// Dashboard -.
func (uc *UseCase) Dashboard(ctx context.Context, channelID string, r entity.Range) (entity.Dashboard, error) {
	d, ok := r.Duration()
	if !ok {
		return entity.Dashboard{}, entity.ErrInvalidState
	}

	out := entity.Dashboard{Range: r}

	cur, err := uc.streams.Current(ctx, channelID)
	if err != nil {
		return entity.Dashboard{}, fmt.Errorf("StreamUseCase - Dashboard - current: %w", err)
	}

	if cur != nil {
		items := []entity.Stream{*cur}
		uc.attachViewerCounts(ctx, items)
		out.Current = &items[0]
	}

	if out.Recent, err = uc.streams.RecentEnded(ctx, channelID, entity.RecentStreamsMax); err != nil {
		return entity.Dashboard{}, fmt.Errorf("StreamUseCase - Dashboard - recent: %w", err)
	}

	if out.Stats, err = uc.streams.DashboardStats(ctx, channelID, uc.now().Add(-d)); err != nil {
		return entity.Dashboard{}, fmt.Errorf("StreamUseCase - Dashboard - stats: %w", err)
	}

	return out, nil
}

// StudioVODs lists the channel's READY and EXPIRED VODs for the Studio.
func (uc *UseCase) StudioVODs(ctx context.Context, channelID string, f repo.StudioVODFilter, cursor string, limit int) (usecase.VODPage, error) {
	return uc.vodPage(ctx, "StudioVODs", cursor, limit, func(after *repo.VODCursor, n int) ([]entity.VOD, error) {
		return uc.vods.ListByChannel(ctx, channelID, false, f, after, n)
	})
}

// UpdateVOD -.
func (uc *UseCase) UpdateVOD(ctx context.Context, channelID, vodID string, p entity.VODPatch) (entity.VOD, error) {
	v, err := uc.vods.UpdateVOD(ctx, channelID, vodID, p)
	if errors.Is(err, entity.ErrNotFound) || errors.Is(err, entity.ErrInvalidState) {
		return entity.VOD{}, err
	}

	if err != nil {
		return entity.VOD{}, fmt.Errorf("StreamUseCase - UpdateVOD: %w", err)
	}

	uc.l.Info("audit vod_updated vod=%s channel=%s visibility=%s", v.ID, channelID, v.Visibility)

	return v, nil
}

// DeleteVOD -.
func (uc *UseCase) DeleteVOD(ctx context.Context, channelID, vodID string) error {
	err := uc.vods.DeleteVOD(ctx, channelID, vodID)
	if errors.Is(err, entity.ErrNotFound) {
		return err
	}

	if err != nil {
		return fmt.Errorf("StreamUseCase - DeleteVOD: %w", err)
	}

	uc.l.Info("audit vod_deleted vod=%s channel=%s", vodID, channelID)

	return nil
}
