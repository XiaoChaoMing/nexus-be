// Package vods removes the files of deleted VODs (vod.deleted.v1 from stream-svc).
package vods

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/eventbus"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/media/internal/storage"
)

// Remover deletes every object under a prefix (storage.MinIO).
type Remover interface {
	RemovePrefix(ctx context.Context, prefix string) (int, error)
}

// Counter counts removed objects (metrics.Metrics).
type Counter interface {
	VODObjectsDeleted(n int)
}

// DeletedHandler removes streams/{streamId}/ of a deleted VOD. A storage error is returned so JetStream
// redelivers the event; a second delivery finds nothing left and is acked.
func DeletedHandler(r Remover, c Counter, l logger.Interface) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		var p events.VODDeleted
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("%w: %w", eventbus.ErrSkip, err)
		}

		// The id becomes an object prefix: anything but a uuid could reach other streams' files.
		u, err := uuid.Parse(p.StreamID)
		if err != nil {
			return fmt.Errorf("%w: bad streamId", eventbus.ErrSkip)
		}

		n, err := r.RemovePrefix(ctx, storage.StreamsPrefix+u.String()+"/")
		if err != nil {
			return fmt.Errorf("vods - remove %s: %w", u, err)
		}

		c.VODObjectsDeleted(n)
		l.Info("media - vod %s deleted: %d objects of stream %s removed", p.VODID, n, u)

		return nil
	}
}
