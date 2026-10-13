// Package usecase declares channel-svc use cases.
package usecase

import (
	"context"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
)

type (
	// Directory is the public read side of channels.
	Directory interface {
		// ByHandle returns entity.ErrNotFound for an unknown or malformed handle and
		// entity.ErrSuspended for a suspended channel.
		ByHandle(ctx context.Context, handle string) (entity.Channel, error)
	}

	// Sync applies events of other services to channels; every method is idempotent on eventID.
	Sync interface {
		// CreatorApproved creates the creator's channel once (owner_id is unique).
		CreatorApproved(ctx context.Context, eventID string, in CreatorApproved) error
		StreamStarted(ctx context.Context, eventID, channelID, streamID string) error
		StreamEnded(ctx context.Context, eventID, channelID, streamID string) error
	}

	// CreatorApproved -.
	CreatorApproved struct {
		UserID      string
		Username    string
		DisplayName string
	}
)
