package streams

import (
	"context"
	"fmt"

	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
)

// _cancelBatch bounds one run of CancelLate.
const _cancelBatch = 100

// OccurrenceUpcoming creates the SCHEDULED stream of the occurrence (idempotent).
func (uc *UseCase) OccurrenceUpcoming(ctx context.Context, eventID string, e events.ScheduleOccurrenceUpcoming) error {
	if err := uc.streams.CreateScheduled(ctx, eventID, e); err != nil {
		return fmt.Errorf("StreamUseCase - OccurrenceUpcoming: %w", err)
	}

	return nil
}

// OccurrenceChanged cancels or moves the SCHEDULED stream of the occurrence.
func (uc *UseCase) OccurrenceChanged(ctx context.Context, eventID string, e events.ScheduleOccurrenceChanged) error {
	if err := uc.streams.ChangeScheduled(ctx, eventID, e); err != nil {
		return fmt.Errorf("StreamUseCase - OccurrenceChanged: %w", err)
	}

	return nil
}

// ScheduleUpdated copies a new title / category to the schedule's SCHEDULED streams.
func (uc *UseCase) ScheduleUpdated(ctx context.Context, eventID string, e events.ScheduleUpdated) error {
	if err := uc.streams.RetitleScheduled(ctx, eventID, e); err != nil {
		return fmt.Errorf("StreamUseCase - ScheduleUpdated: %w", err)
	}

	return nil
}

// CancelLate -.
func (uc *UseCase) CancelLate(ctx context.Context) (int, error) {
	n, err := uc.streams.CancelLateScheduled(ctx, uc.late, _cancelBatch)
	if err != nil {
		return 0, fmt.Errorf("StreamUseCase - CancelLate: %w", err)
	}

	if n > 0 {
		uc.obs.Transition(entity.StatusCancelled)
		uc.l.Info("cancelled %d scheduled streams not started within %s", n, uc.late)
	}

	return n, nil
}
