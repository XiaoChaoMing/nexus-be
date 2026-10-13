// Package repo declares stream-svc storage.
package repo

import (
	"context"
	"time"

	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
)

//go:generate mockgen -source=contracts.go -destination=../usecase/mocks_repo_test.go -package=usecase_test

type (
	// KeyRepo -.
	KeyRepo interface {
		// ChannelByKeyHash returns the channel of an active (not revoked) key.
		ChannelByKeyHash(ctx context.Context, keyHash string) (string, error)
		// Replace revokes the channel's active key and stores the new hash.
		Replace(ctx context.Context, channelID, keyHash string) (time.Time, error)
		Info(ctx context.Context, channelID string) (entity.StreamKeyInfo, error)
	}

	// SettingsRepo -.
	SettingsRepo interface {
		// GetSettings returns defaults when the channel never saved settings.
		GetSettings(ctx context.Context, channelID string) (entity.Settings, error)
		// SaveSettings upserts the settings and applies them to the channel's on-air stream, if any.
		SaveSettings(ctx context.Context, s entity.Settings) error
	}

	// StreamRepo owns the NXL-A §7A state machine. Every transition is a conditional UPDATE in one
	// transaction with its outbox event, so concurrent callers cannot both win.
	StreamRepo interface {
		// Authorize resumes an INTERRUPTED stream still inside grace, ends an expired one, or creates a LIVE stream.
		Authorize(ctx context.Context, channelID string, grace time.Duration) (entity.Grant, error)
		// End moves the channel's on-air stream to ENDED.
		End(ctx context.Context, channelID string, reason entity.EndReason) (entity.Stream, error)
		// ExpireInterrupted ends every INTERRUPTED stream older than grace.
		ExpireInterrupted(ctx context.Context, grace time.Duration) ([]entity.Stream, error)

		// Event handlers: eventID de-duplicates through processed_events (empty = no de-dup).
		OnIngestStarted(ctx context.Context, eventID, streamID string) error
		OnIngestStopped(ctx context.Context, eventID, streamID string) error
		OnVODProcessed(ctx context.Context, eventID string, v VODInput) error

		Get(ctx context.Context, streamID string) (entity.Stream, error)
		Current(ctx context.Context, channelID string) (*entity.Stream, error)
		ListOnAir(ctx context.Context, after *Cursor, limit int) ([]entity.Stream, error)
		GetVOD(ctx context.Context, vodID string) (entity.VOD, error)

		// EndedByCreatorSince reports whether the channel's latest stream was ended by the creator after since.
		EndedByCreatorSince(ctx context.Context, channelID string, since time.Time) (bool, error)
		// ExpireVODs marks READY VODs past expires_at as EXPIRED; returns how many.
		ExpireVODs(ctx context.Context) (int64, error)
		// CountOnAir returns how many streams are LIVE or INTERRUPTED.
		CountOnAir(ctx context.Context) (int, error)
	}

	// ChannelRepo is the local copy of channel-svc's channels (creator_channels).
	ChannelRepo interface {
		// ChannelOfOwner returns entity.ErrNotFound when the user owns no channel.
		ChannelOfOwner(ctx context.Context, ownerID string) (entity.Channel, error)
		// ChannelsByID returns the known channels among ids in one query, keyed by channel id.
		ChannelsByID(ctx context.Context, ids []string) (map[string]entity.Channel, error)
		// UpsertChannel stores the channel of c.OwnerID; eventID de-duplicates (empty = no de-dup).
		UpsertChannel(ctx context.Context, eventID string, c entity.Channel) error
	}

	// VODInput -.
	VODInput struct {
		StreamID     string
		DurationSec  int
		PlaylistPath string
		ThumbnailURL string
		Retention    time.Duration
	}

	// Cursor is the keyset position of ListOnAir (started_at DESC, id DESC).
	Cursor struct {
		StartedAt time.Time
		ID        string
	}
)
