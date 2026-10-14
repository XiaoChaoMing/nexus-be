// Package playback opens playback sessions behind the age gate and keeps signed-in viewers' VOD positions.
package playback

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

const (
	_defaultContinue = 12
	_maxContinue     = 50
	// Expired sessions are kept a day for troubleshooting before they are purged.
	_sessionRetention = 24 * time.Hour
)

// UseCase implements usecase.Viewer.
type UseCase struct {
	streams  repo.StreamRepo
	channels repo.ChannelRepo
	viewers  repo.ViewerRepo
	now      func() time.Time
	l        logger.Interface
}

var _ usecase.Viewer = (*UseCase)(nil)

// New -.
func New(streams repo.StreamRepo, channels repo.ChannelRepo, viewers repo.ViewerRepo, l logger.Interface) *UseCase {
	return &UseCase{
		streams: streams, channels: channels, viewers: viewers, l: l,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// WithClock replaces the time source; used by tests.
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

type media struct {
	channelID string
	objectKey string
	mature    bool
}

// Open resolves the stream or VOD, applies the age gate and records a playback session.
// A closed gate is not an error: the grant says so and carries no media.
func (uc *UseCase) Open(ctx context.Context, in usecase.PlaybackInput) (entity.PlaybackGrant, error) {
	m, err := uc.media(ctx, in.Target, in.UserID)
	if err != nil {
		return entity.PlaybackGrant{}, err
	}

	grant := entity.PlaybackGrant{}

	if in.UserID == "" {
		grant.AnonID = in.AnonID
		if grant.AnonID == "" {
			grant.AnonID = uuid.NewString()
		}
	}

	grant.Mature, err = uc.gate(ctx, in, m)
	if err != nil {
		return entity.PlaybackGrant{}, err
	}

	if !grant.Mature.Open() {
		return grant, nil
	}

	id, err := uuid.NewV7()
	if err != nil {
		return entity.PlaybackGrant{}, fmt.Errorf("PlaybackUseCase - Open - uuid: %w", err)
	}

	now := uc.now()
	s := entity.PlaybackSession{
		ID: id.String(), Target: in.Target, UserID: in.UserID, AnonID: grant.AnonID,
		MatureAcknowledged: grant.Mature.Required, CreatedAt: now, ExpiresAt: now.Add(entity.PlaybackTTL),
	}

	if err = uc.viewers.CreatePlaybackSession(ctx, s); err != nil {
		return entity.PlaybackGrant{}, fmt.Errorf("PlaybackUseCase - Open - session: %w", err)
	}

	grant.Session = &s
	grant.ObjectKey = m.objectKey

	return grant, nil
}

// gate decides the mature outcome. Signed-in viewers who acknowledge once are not asked again for that channel.
func (uc *UseCase) gate(ctx context.Context, in usecase.PlaybackInput, m media) (entity.MatureGate, error) {
	if !m.mature {
		return entity.MatureGate{}, nil
	}

	g := entity.MatureGate{Required: true, Acknowledged: in.AcknowledgeMature}

	if in.UserID == "" {
		return g, nil
	}

	if in.AcknowledgeMature {
		if err := uc.viewers.SaveMatureConsent(ctx, in.UserID, m.channelID); err != nil {
			return entity.MatureGate{}, fmt.Errorf("PlaybackUseCase - gate - save consent: %w", err)
		}

		return g, nil
	}

	ok, err := uc.viewers.HasMatureConsent(ctx, in.UserID, m.channelID)
	if err != nil {
		return entity.MatureGate{}, fmt.Errorf("PlaybackUseCase - gate - consent: %w", err)
	}

	g.Acknowledged = ok

	return g, nil
}

func (uc *UseCase) media(ctx context.Context, t entity.PlaybackTarget, userID string) (media, error) {
	if t.VODID == "" {
		s, err := uc.streams.Get(ctx, t.StreamID)
		if err != nil {
			return media{}, err
		}

		return media{channelID: s.ChannelID, objectKey: "streams/" + s.ID + "/master.m3u8", mature: s.IsMature}, nil
	}

	v, err := uc.streams.GetVOD(ctx, t.VODID)
	if err != nil {
		return media{}, err
	}

	if v.Hidden() {
		if err := uc.ownerOnly(ctx, userID, v.ChannelID); err != nil {
			return media{}, err
		}
	}

	// A VOD inherits the mature flag of the broadcast it records.
	s, err := uc.streams.Get(ctx, v.StreamID)
	if err != nil {
		return media{}, fmt.Errorf("PlaybackUseCase - media - stream of vod: %w", err)
	}

	return media{channelID: v.ChannelID, objectKey: v.PlaylistPath, mature: s.IsMature}, nil
}

// ownerOnly returns entity.ErrNotFound unless userID owns channelID (hidden VODs, BR-G2-07).
func (uc *UseCase) ownerOnly(ctx context.Context, userID, channelID string) error {
	if userID == "" {
		return entity.ErrNotFound
	}

	ch, err := uc.channels.ChannelOfOwner(ctx, userID)
	if errors.Is(err, entity.ErrNotFound) || (err == nil && ch.ID != channelID) {
		return entity.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("PlaybackUseCase - owner: %w", err)
	}

	return nil
}

// SaveProgress -.
func (uc *UseCase) SaveProgress(ctx context.Context, p entity.WatchProgress, keepFurther bool) error {
	v, err := uc.streams.GetVOD(ctx, p.VODID)
	if err != nil {
		return err
	}

	if !entity.ValidPosition(p.PositionSec, v.DurationSec) {
		return entity.ErrInvalidPosition
	}

	p.UpdatedAt = uc.now()

	if err = uc.viewers.SaveProgress(ctx, p, keepFurther); err != nil {
		return fmt.Errorf("PlaybackUseCase - SaveProgress: %w", err)
	}

	return nil
}

// Progress -.
func (uc *UseCase) Progress(ctx context.Context, userID, vodID string) (*int, error) {
	pos, err := uc.viewers.Progress(ctx, userID, vodID)
	if err != nil {
		return nil, fmt.Errorf("PlaybackUseCase - Progress: %w", err)
	}

	return pos, nil
}

// ContinueWatching -.
func (uc *UseCase) ContinueWatching(ctx context.Context, userID string, limit int) ([]entity.ContinueItem, error) {
	if limit <= 0 || limit > _maxContinue {
		limit = _defaultContinue
	}

	items, err := uc.viewers.ContinueWatching(ctx, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("PlaybackUseCase - ContinueWatching: %w", err)
	}

	if len(items) == 0 {
		return items, nil
	}

	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.VOD.ChannelID)
	}

	byID, err := uc.channels.ChannelsByID(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("PlaybackUseCase - ContinueWatching - channels: %w", err)
	}

	for i := range items {
		if ch, ok := byID[items[i].VOD.ChannelID]; ok {
			items[i].VOD.Channel = &ch
		}
	}

	return items, nil
}

// PurgeSessions -.
func (uc *UseCase) PurgeSessions(ctx context.Context) (int64, error) {
	n, err := uc.viewers.PurgePlaybackSessions(ctx, uc.now().Add(-_sessionRetention))
	if err != nil {
		return 0, fmt.Errorf("PlaybackUseCase - PurgeSessions: %w", err)
	}

	return n, nil
}
