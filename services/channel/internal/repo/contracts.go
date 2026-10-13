// Package repo declares channel-svc storage.
package repo

import (
	"context"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
)

//go:generate mockgen -source=contracts.go -destination=../usecase/mocks_repo_test.go -package=usecase_test

type (
	// ChannelRepo stores channels. Event methods de-duplicate on eventID through processed_events
	// (empty = no de-dup) and write their outgoing event to the outbox in the same transaction.
	ChannelRepo interface {
		// ByHandle looks the handle up case-insensitively; entity.ErrNotFound when missing.
		ByHandle(ctx context.Context, handle string) (entity.Channel, error)
		// CreateForOwner inserts the owner's channel with the first free handle candidate of
		// in.BaseHandle and queues channel.created. created is false when the owner already has a
		// channel (returned as is, no event) or the event was already processed (zero channel).
		CreateForOwner(ctx context.Context, eventID string, in NewChannel) (ch entity.Channel, created bool, err error)
		// SetLive marks the channel live with streamID.
		SetLive(ctx context.Context, eventID, channelID, streamID string) error
		// SetOffline clears the live flag unless a newer stream already replaced streamID.
		SetOffline(ctx context.Context, eventID, channelID, streamID string) error
	}

	// NewChannel -.
	NewChannel struct {
		OwnerID     string
		BaseHandle  string
		DisplayName string
	}
)
