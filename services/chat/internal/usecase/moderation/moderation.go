// Package moderation implements the moderation tools of chat-svc (NXL-F §3.1–3.6): deleting and pinning
// messages, timeouts and bans, the user card, blocked terms, moderator / VIP roles and report evidence.
package moderation

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

const (
	// _cacheTTL is ROLE_CACHE_TTL and AUTOMOD_CACHE_TTL: a change made on another instance applies within it.
	_cacheTTL     = 5 * time.Second
	_defaultLimit = 24
	_maxLimit     = 50
)

// Publisher is the realtime fan-out (fanout.Publisher).
type Publisher interface {
	Publish(ctx context.Context, topic entity.Topic, typ string, data any) (entity.Event, error)
}

// Repos groups the storage the use case needs.
type Repos struct {
	Rooms    repo.RoomRepo
	Mod      repo.ModerationRepo
	Terms    repo.TermRepo
	Roles    repo.RoleRepo
	Users    repo.UserRepo
	Audience repo.AudienceStore
	Live     repo.LiveStore
}

// Config -.
type Config struct {
	// TimeoutMax is CHAT_TIMEOUT_MAX (14 days).
	TimeoutMax time.Duration
}

// UseCase implements usecase.Moderation, usecase.Roles, usecase.Evidence and chat.AutoModSource / PinSource.
type UseCase struct {
	r     Repos
	pub   Publisher
	cfg   Config
	l     logger.Interface
	now   func() time.Time
	roles *ttlCache
	terms *ttlCache
}

var (
	_ usecase.Moderation = (*UseCase)(nil)
	_ usecase.Roles      = (*UseCase)(nil)
	_ usecase.Evidence   = (*UseCase)(nil)
)

// New -.
func New(r Repos, pub Publisher, cfg Config, l logger.Interface) *UseCase {
	return &UseCase{
		r: r, pub: pub, cfg: cfg, l: l, now: time.Now,
		roles: &ttlCache{ttl: _cacheTTL}, terms: &ttlCache{ttl: _cacheTTL},
	}
}

// WithClock -.
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

// ---- actors --------------------------------------------------------------------------------------

// actor returns the caller's actor role in the channel: owner, then admin, then moderator (BR-F01).
func (uc *UseCase) actor(ctx context.Context, p entity.Principal, channelID string) (entity.ActorRole, entity.Channel, error) {
	if p.Guest() {
		return "", entity.Channel{}, entity.ErrUnauthenticated
	}

	ch, err := uc.r.Users.Channel(ctx, channelID)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return "", entity.Channel{}, err
		}

		return "", entity.Channel{}, fmt.Errorf("ModerationUseCase - channel: %w", err)
	}

	switch {
	case ch.OwnerID == p.UserID:
		return entity.ActorOwner, ch, nil
	case p.IsAdmin():
		return entity.ActorAdmin, ch, nil
	}

	role, err := uc.roleOf(ctx, channelID, p.UserID)
	if err != nil {
		return "", entity.Channel{}, err
	}

	if role == entity.ChannelRoleModerator {
		return entity.ActorModerator, ch, nil
	}

	return "", entity.Channel{}, entity.ErrForbidden
}

// manager is the actor of role management: the owner or an admin (moderators manage chat, not roles).
func (uc *UseCase) manager(ctx context.Context, p entity.Principal, channelID string) (entity.ActorRole, entity.Channel, error) {
	role, ch, err := uc.actor(ctx, p, channelID)
	if err != nil {
		return "", entity.Channel{}, err
	}

	if role == entity.ActorModerator {
		return "", entity.Channel{}, entity.ErrForbidden
	}

	return role, ch, nil
}

func (uc *UseCase) roleOf(ctx context.Context, channelID, userID string) (entity.ChannelRole, error) {
	key := channelID + "/" + userID
	if v, ok := uc.roles.get(key, uc.now()); ok {
		role, _ := v.(entity.ChannelRole) //nolint:errcheck // only ChannelRole values are stored

		return role, nil
	}

	roles, err := uc.r.Roles.Roles(ctx, channelID, []string{userID})
	if err != nil {
		return "", fmt.Errorf("ModerationUseCase - roles: %w", err)
	}

	uc.roles.set(key, roles[userID], uc.now())

	return roles[userID], nil
}

// protected applies the target rules of timeouts and bans: never oneself, never the owner, a moderator only
// by an admin (BR-F02).
func (uc *UseCase) protected(ctx context.Context, actor entity.ActorRole, ch entity.Channel, actorID, targetID string) error {
	switch {
	case targetID == actorID:
		return entity.SelfTarget()
	case targetID == ch.OwnerID:
		return entity.Protected()
	case actor == entity.ActorAdmin:
		return nil
	}

	role, err := uc.roleOf(ctx, ch.ID, targetID)
	if err != nil {
		return err
	}

	if role == entity.ChannelRoleModerator {
		return entity.Protected()
	}

	return nil
}

// knownUser returns entity.ErrNotFound for a user the identity copy does not have.
func (uc *UseCase) knownUser(ctx context.Context, userID string) (entity.User, error) {
	users, err := uc.r.Users.Users(ctx, []string{userID})
	if err != nil {
		return entity.User{}, fmt.Errorf("ModerationUseCase - user: %w", err)
	}

	u, ok := users[userID]
	if !ok {
		return entity.User{}, entity.ErrNotFound
	}

	return u, nil
}

// room returns the stream's room with its channel owner.
func (uc *UseCase) room(ctx context.Context, streamID string) (entity.SendContext, error) {
	sc, err := uc.r.Rooms.SendContext(ctx, streamID, "")
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return entity.SendContext{}, err
		}

		return entity.SendContext{}, fmt.Errorf("ModerationUseCase - room: %w", err)
	}

	return sc, nil
}

// relay publishes a realtime event after the change was committed; a failure is only logged (viewers catch up
// on their next resync).
func (uc *UseCase) relay(ctx context.Context, topic entity.Topic, typ string, data any) {
	if _, err := uc.pub.Publish(ctx, topic, typ, data); err != nil {
		uc.l.Warn("moderation - %s on %s: %v", typ, topic, err)
	}
}

// ---- cursors -------------------------------------------------------------------------------------

func pageLimit(limit int) int {
	if limit <= 0 || limit > _maxLimit {
		return _defaultLimit
	}

	return limit
}

func encodeCursor(at time.Time, id string) *string {
	s := base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))

	return &s
}

func decodeCursor(s string) (*repo.Cursor, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // first page
	}

	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, entity.Reject(entity.ErrValidation, "cursor", entity.DetailInvalid)
	}

	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || uuid.Validate(id) != nil {
		return nil, entity.Reject(entity.ErrValidation, "cursor", entity.DetailInvalid)
	}

	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, entity.Reject(entity.ErrValidation, "cursor", entity.DetailInvalid)
	}

	return &repo.Cursor{CreatedAt: t, ID: id}, nil
}
