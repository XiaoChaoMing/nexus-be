// Package gateway implements what the realtime gateway needs besides chat itself.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/jwt"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

// Verifier checks access tokens (jwt.JWKSVerifier).
type Verifier interface {
	Verify(token string) (*jwt.Claims, error)
}

// UseCase implements usecase.Gateway.
type UseCase struct {
	verifier Verifier
	chat     usecase.Chat
	viewers  usecase.Viewers
	seq      repo.SeqStore
	presence repo.PresenceStore
	broker   repo.Broker
	audience repo.AudienceStore
	live     repo.LiveStore
	locks    repo.LockStore
	maxConns int
	l        logger.Interface
}

var _ usecase.Gateway = (*UseCase)(nil)

// Deps groups the dependencies of the gateway use case.
type Deps struct {
	Verifier Verifier
	Chat     usecase.Chat
	Viewers  usecase.Viewers
	Seq      repo.SeqStore
	Presence repo.PresenceStore
	Broker   repo.Broker
	Audience repo.AudienceStore
	Live     repo.LiveStore
	// Locks may be nil (no lock check).
	Locks repo.LockStore
}

// New -.
func New(d Deps, maxConnsPerUser int, l logger.Interface) *UseCase {
	return &UseCase{
		verifier: d.Verifier, chat: d.Chat, viewers: d.Viewers, seq: d.Seq, presence: d.Presence, broker: d.Broker,
		audience: d.Audience, live: d.Live, locks: d.Locks, maxConns: maxConnsPerUser, l: l,
	}
}

// Authorize -. A locked account's still valid token is refused with entity.ErrAccountLocked (close 4003); when
// Redis cannot tell, the token is accepted: the lock already closed every socket of the account.
func (uc *UseCase) Authorize(ctx context.Context, token *string, anonID string) (entity.Principal, error) {
	p := entity.Principal{}

	// A malformed anonId is ignored: the guest is then counted per connection.
	if u, err := uuid.Parse(anonID); err == nil {
		p.AnonID = u.String()
	}

	if token == nil {
		return p, nil
	}

	claims, err := uc.verifier.Verify(*token)
	if err != nil || claims.Subject == "" || claims.ExpiresAt == nil {
		return entity.Principal{}, entity.ErrInvalidToken
	}

	p.UserID, p.GlobalRole, p.ExpiresAt = claims.Subject, claims.Role, claims.ExpiresAt.Time

	if uc.locks != nil {
		locked, lerr := uc.locks.Locked(ctx, p.UserID)
		if lerr != nil {
			uc.l.Warn("gateway - lock check of %s: %v", p.UserID, lerr)
		}

		if locked {
			return entity.Principal{}, entity.ErrAccountLocked
		}
	}

	return p, nil
}

// ConnOpened -.
func (uc *UseCase) ConnOpened(ctx context.Context, p entity.Principal) error {
	if p.Guest() {
		return nil
	}

	ok, err := uc.presence.AddConn(ctx, p.UserID, p.ConnID, uc.maxConns)
	if err != nil {
		return fmt.Errorf("GatewayUseCase - ConnOpened: %w", err)
	}

	if !ok {
		return entity.ErrTooManyConnections
	}

	return nil
}

// ConnAlive -.
func (uc *UseCase) ConnAlive(ctx context.Context, p entity.Principal) {
	if p.Guest() {
		return
	}

	if err := uc.presence.TouchConn(ctx, p.UserID, p.ConnID); err != nil {
		uc.l.Warn("gateway - touch conn: %v", err)
	}
}

// ConnClosed -.
func (uc *UseCase) ConnClosed(ctx context.Context, p entity.Principal) {
	if p.Guest() {
		return
	}

	if err := uc.presence.RemoveConn(ctx, p.UserID, p.ConnID); err != nil {
		uc.l.Warn("gateway - remove conn: %v", err)
	}
}

// Join -.
func (uc *UseCase) Join(ctx context.Context, p entity.Principal, topic entity.Topic) (any, error) {
	switch topic.Kind {
	case entity.TopicUser, entity.TopicSidebar:
		if p.Guest() || topic.ID != p.UserID {
			return nil, entity.ErrForbidden
		}

		return nil, nil
	case entity.TopicChat:
		snap, err := uc.chat.Snapshot(ctx, p, topic.ID)
		if err != nil {
			return nil, fmt.Errorf("GatewayUseCase - Join - chat: %w", err)
		}

		if _, err = uc.viewers.Join(ctx, topic.ID, p.ViewerKey()); err != nil {
			uc.l.Warn("gateway - viewer join: %v", err)
		}

		return snap, nil
	case entity.TopicStream:
		return uc.streamSnapshot(ctx, p, topic.ID), nil
	}

	return nil, entity.ErrInvalidTopic
}

// streamSnapshot degrades to an unknown status and a zero count when Redis is down: reading stays up.
func (uc *UseCase) streamSnapshot(ctx context.Context, p entity.Principal, streamID string) usecase.StreamSnapshot {
	snap := usecase.StreamSnapshot{}

	n, err := uc.viewers.Join(ctx, streamID, p.ViewerKey())
	if err != nil {
		uc.l.Warn("gateway - viewer join: %v", err)
	}

	snap.ViewerCount = n

	status, err := uc.presence.StreamStatus(ctx, streamID)
	if err != nil {
		uc.l.Warn("gateway - stream status: %v", err)
	}

	if status != "" {
		snap.Status = &status
	}

	return snap
}

// Leave -.
func (uc *UseCase) Leave(p entity.Principal, topic entity.Topic) {
	if id := topic.StreamID(); id != "" {
		uc.viewers.Leave(id, p.ViewerKey())
	}
}

// Seq -.
func (uc *UseCase) Seq(ctx context.Context, topic string) (int64, error) {
	n, err := uc.seq.Seq(ctx, topic)
	if err != nil {
		return 0, fmt.Errorf("GatewayUseCase - Seq: %w", err)
	}

	return n, nil
}

// Since -.
func (uc *UseCase) Since(ctx context.Context, topic string, after int64, limit int) ([]entity.Event, bool, error) {
	evs, ok, err := uc.seq.Since(ctx, topic, after, limit)
	if err != nil {
		return nil, false, fmt.Errorf("GatewayUseCase - Since: %w", err)
	}

	return evs, ok, nil
}

// Listen -.
func (uc *UseCase) Listen(topic string, fn func(entity.Event)) (func(), error) {
	stop, err := uc.broker.Subscribe(topic, func(payload []byte) {
		var ev entity.Event
		if err := json.Unmarshal(payload, &ev); err != nil || ev.Topic != topic || ev.Seq <= 0 {
			uc.l.Warn("gateway - bad realtime payload on %s", topic)

			return
		}

		fn(ev)
	})
	if err != nil {
		return nil, fmt.Errorf("GatewayUseCase - Listen %s: %w", topic, err)
	}

	return stop, nil
}

// SidebarCounts -.
func (uc *UseCase) SidebarCounts(ctx context.Context, userID string) ([]entity.SidebarCount, error) {
	channels, err := uc.audience.Following(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("GatewayUseCase - SidebarCounts - following: %w", err)
	}

	live, err := uc.live.Live(ctx, channels)
	if err != nil {
		return nil, fmt.Errorf("GatewayUseCase - SidebarCounts - live: %w", err)
	}

	if len(live) == 0 {
		return nil, nil
	}

	streamIDs := make([]string, 0, len(live))
	for _, l := range live {
		streamIDs = append(streamIDs, l.StreamID)
	}

	counts, err := uc.presence.ViewerCounts(ctx, streamIDs)
	if err != nil {
		return nil, fmt.Errorf("GatewayUseCase - SidebarCounts - counts: %w", err)
	}

	out := make([]entity.SidebarCount, 0, len(live))
	for _, ch := range channels {
		if l, ok := live[ch]; ok {
			out = append(out, entity.SidebarCount{ChannelID: ch, StreamID: l.StreamID, Count: counts[l.StreamID]})
		}
	}

	return out, nil
}

// TokenGrace is how long a connection may stay open after its access token expired (NXL-00 §10.1).
const TokenGrace = 60 * time.Second
