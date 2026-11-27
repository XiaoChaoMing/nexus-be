// Package vods removes the files of deleted VODs (vod.deleted.v1 from stream-svc).
package vods

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

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

// _prefixRe is the only shape of PlaylistPrefix accepted: the prefix is handed to a recursive delete.
var _prefixRe = regexp.MustCompile(`^(?:streams|videos)/([0-9a-f-]{36})/$`)

// DeletedHandler removes the objects of a deleted VOD: PlaylistPrefix (streams/{streamId}/ or videos/{vodId}/),
// or streams/{streamId}/ for events published before PlaylistPrefix existed. A storage error is returned so
// JetStream redelivers the event; a second delivery finds nothing left and is acked.
func DeletedHandler(r Remover, c Counter, l logger.Interface) eventbus.Handler {
	return func(ctx context.Context, e eventbus.Event) error {
		var p events.VODDeleted
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("%w: %w", eventbus.ErrSkip, err)
		}

		prefix, err := playlistPrefix(p)
		if err != nil {
			return err
		}

		n, err := r.RemovePrefix(ctx, prefix)
		if err != nil {
			return fmt.Errorf("vods - remove %s: %w", prefix, err)
		}

		c.VODObjectsDeleted(n)
		l.Info("media - vod %s deleted: %d objects under %s removed", p.VODID, n, prefix)

		return nil
	}
}

// playlistPrefix validates the prefix to delete. The id inside becomes an object prefix: anything but a
// uuid could reach other streams' or videos' files.
func playlistPrefix(p events.VODDeleted) (string, error) {
	if p.PlaylistPrefix != "" {
		m := _prefixRe.FindStringSubmatch(p.PlaylistPrefix)
		if m == nil {
			return "", fmt.Errorf("%w: bad playlistPrefix", eventbus.ErrSkip)
		}

		if _, err := uuid.Parse(m[1]); err != nil {
			return "", fmt.Errorf("%w: bad playlistPrefix id", eventbus.ErrSkip)
		}

		return p.PlaylistPrefix, nil
	}

	u, err := uuid.Parse(p.StreamID)
	if err != nil {
		return "", fmt.Errorf("%w: bad streamId", eventbus.ErrSkip)
	}

	return storage.StreamsPrefix + u.String() + "/", nil
}
