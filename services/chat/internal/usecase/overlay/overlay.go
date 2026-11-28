// Package overlay serves the OBS overlay options of a channel (chat theme, gift alert) and the alerts a
// streamer sends to test them.
package overlay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

const (
	// _testGap is the least time between two test alerts of one channel.
	_testGap = time.Second
	// _testMemory bounds the per-channel throttle table; it is emptied when full.
	_testMemory = 4096

	_defaultTestUser    = "viewer_demo"
	_defaultTestItem    = "Hoa hồng"
	_defaultTestIcon    = "rose"
	_defaultTestQty     = 1
	_defaultTestTotal   = 10_000
	_defaultTestMessage = "Chúc stream thật vui nhé!"
)

// Publisher is the realtime fan-out (fanout.Publisher).
type Publisher interface {
	Publish(ctx context.Context, topic entity.Topic, typ string, data any) (entity.Event, error)
}

// Repos groups the storage the overlay needs.
type Repos struct {
	Overlay repo.OverlayRepo
	Users   repo.UserRepo
}

// UseCase implements usecase.Overlay.
type UseCase struct {
	r   Repos
	pub Publisher
	l   logger.Interface
	now func() time.Time

	mu     sync.Mutex
	lastAt map[string]time.Time
}

var _ usecase.Overlay = (*UseCase)(nil)

// Option -.
type Option func(*UseCase)

// WithClock -.
func WithClock(now func() time.Time) Option { return func(uc *UseCase) { uc.now = now } }

// New -.
func New(r Repos, pub Publisher, l logger.Interface, opts ...Option) *UseCase {
	uc := &UseCase{r: r, pub: pub, l: l, now: time.Now, lastAt: map[string]time.Time{}}
	for _, o := range opts {
		o(uc)
	}

	return uc
}

// Get returns the channel's options, the defaults when it never saved any; entity.ErrNotFound for an unknown channel.
func (uc *UseCase) Get(ctx context.Context, channelID string) (entity.OverlaySettings, error) {
	s, err := uc.r.Overlay.Overlay(ctx, channelID)
	if err != nil {
		return entity.OverlaySettings{}, fmt.Errorf("OverlayUseCase - Get: %w", err)
	}

	if s.UpdatedAt != nil {
		return s, nil
	}

	if _, err = uc.r.Users.Channel(ctx, channelID); err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return entity.OverlaySettings{}, err
		}

		return entity.OverlaySettings{}, fmt.Errorf("OverlayUseCase - Get - channel: %w", err)
	}

	return s, nil
}

// Update replaces the options (channel owner or admin) and restyles every overlay page that is open.
func (uc *UseCase) Update(ctx context.Context, p entity.Principal, s entity.OverlaySettings) (entity.OverlaySettings, error) {
	if p.Guest() {
		return entity.OverlaySettings{}, entity.ErrUnauthenticated
	}

	if err := s.Validate(); err != nil {
		return entity.OverlaySettings{}, err
	}

	if err := uc.canManage(ctx, p, s.ChannelID); err != nil {
		return entity.OverlaySettings{}, err
	}

	s.UpdatedBy = &p.UserID

	saved, err := uc.r.Overlay.SaveOverlay(ctx, s)
	if err != nil {
		return entity.OverlaySettings{}, fmt.Errorf("OverlayUseCase - Update: %w", err)
	}

	// The options are saved: a lost push only means open pages restyle on their next reload.
	if _, err = uc.pub.Publish(ctx, entity.OverlayTopic(saved.ChannelID), entity.EventOverlaySettings, saved); err != nil {
		uc.l.Warn("overlay - publish settings of %s: %v", saved.ChannelID, err)
	}

	return saved, nil
}

// TestGift shows a gift alert on the channel's open overlay pages only (never in chat, never on the ledger).
// Fields left empty take demo values.
func (uc *UseCase) TestGift(ctx context.Context, p entity.Principal, channelID string, g entity.GiftMeta) error {
	if p.Guest() {
		return entity.ErrUnauthenticated
	}

	if err := uc.canManage(ctx, p, channelID); err != nil {
		return err
	}

	if !uc.allowTest(channelID) {
		return entity.ErrRateLimited
	}

	if _, err := uc.pub.Publish(ctx, entity.OverlayTopic(channelID), entity.EventOverlayTestGift, withDemoDefaults(g, p.UserID)); err != nil {
		return fmt.Errorf("OverlayUseCase - TestGift: %w", err)
	}

	return nil
}

// canManage lets the channel owner and admins through: moderators do not style the stream.
func (uc *UseCase) canManage(ctx context.Context, p entity.Principal, channelID string) error {
	if p.IsAdmin() {
		return nil
	}

	ch, err := uc.r.Users.Channel(ctx, channelID)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return err
		}

		return fmt.Errorf("OverlayUseCase - canManage - channel: %w", err)
	}

	if ch.OwnerID != p.UserID {
		return entity.ErrForbidden
	}

	return nil
}

func (uc *UseCase) allowTest(channelID string) bool {
	now := uc.now()

	uc.mu.Lock()
	defer uc.mu.Unlock()

	if last, ok := uc.lastAt[channelID]; ok && now.Sub(last) < _testGap {
		return false
	}

	if len(uc.lastAt) >= _testMemory {
		clear(uc.lastAt)
	}

	uc.lastAt[channelID] = now

	return true
}

func withDemoDefaults(g entity.GiftMeta, callerID string) entity.GiftMeta {
	if g.UserID == "" {
		g.UserID = callerID
	}

	if g.Username == "" {
		g.Username = _defaultTestUser
	}

	if g.ItemName == "" {
		g.ItemName = _defaultTestItem
		g.Icon = _defaultTestIcon
	}

	if g.Quantity < 1 {
		g.Quantity = _defaultTestQty
	}

	if g.Total <= 0 {
		g.Total = _defaultTestTotal
	}

	if g.Message == nil {
		msg := _defaultTestMessage
		g.Message = &msg
	}

	return g
}
