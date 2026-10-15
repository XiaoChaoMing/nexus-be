// Package chat implements sending (NXL-B B1–B3), history, emotes and chat modes.
package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
)

const (
	_emoteCacheTTL  = time.Minute
	_maxHistory     = 100
	_bodyHashBytes  = 16
	_fallbackIDRune = 8

	_rateKeyUser = "chat:rate:"
	_rateKeyIP   = "chat:rate:ip:"
)

// Config holds the NXL-B values (TSD §9).
type Config struct {
	RateLimit           int
	RateLimitPrivileged int
	RateLimitIP         int
	RateWindow          time.Duration
	MaxLines            int
	HistoryDefault      int
	Disabled            bool
	RequireVerified     bool
}

// Publisher is the realtime fan-out (fanout.Publisher).
type Publisher interface {
	Publish(ctx context.Context, topic entity.Topic, typ string, data any) (entity.Event, error)
}

// Repos groups the storage chat needs.
type Repos struct {
	Rooms    repo.RoomRepo
	Messages repo.MessageRepo
	Settings repo.SettingsRepo
	Roles    repo.RoleRepo
	Emotes   repo.EmoteRepo
	Users    repo.UserRepo
	Guard    repo.SendGuard
	Audience repo.AudienceStore
}

// AutoModSource returns a channel's blocked terms in normalized form (cached by the moderation use case).
type AutoModSource interface {
	BlockedTerms(ctx context.Context, channelID string) ([]string, error)
}

// PinSource returns a room's pinned message for the snapshot.
type PinSource interface {
	Pinned(ctx context.Context, streamID string) (*entity.Message, error)
}

// UseCase implements usecase.Chat.
type UseCase struct {
	r       Repos
	pub     Publisher
	obs     usecase.Observer
	automod AutoModSource
	pins    PinSource
	cfg     Config
	l       logger.Interface
	now     func() time.Time

	emoteMu sync.Mutex
	emotes  []entity.Emote
	emoteAt time.Time
}

var _ usecase.Chat = (*UseCase)(nil)

// Option -.
type Option func(*UseCase)

// WithObserver -.
func WithObserver(o usecase.Observer) Option { return func(uc *UseCase) { uc.obs = o } }

// WithClock -.
func WithClock(now func() time.Time) Option { return func(uc *UseCase) { uc.now = now } }

// WithAutoMod checks messages against the channel's blocked terms (and links, from the settings).
func WithAutoMod(src AutoModSource) Option { return func(uc *UseCase) { uc.automod = src } }

// WithPins adds the pinned message to the chat: snapshot.
func WithPins(src PinSource) Option { return func(uc *UseCase) { uc.pins = src } }

// New -.
func New(r Repos, pub Publisher, cfg Config, l logger.Interface, opts ...Option) *UseCase {
	uc := &UseCase{r: r, pub: pub, cfg: cfg, l: l, obs: noopObserver{}, now: time.Now}
	for _, o := range opts {
		o(uc)
	}

	return uc
}

type noopObserver struct{}

func (noopObserver) MessageSent()    {}
func (noopObserver) Rejected(string) {}

// ---- send ----------------------------------------------------------------------------------------

// Send validates, stores and broadcasts one message. A clientMsgId already accepted returns the stored
// message id with Duplicate set, so a retry after a lost ack never creates a second message (BR07).
func (uc *UseCase) Send(ctx context.Context, p entity.Principal, streamID string, in entity.SendInput) (entity.SendResult, error) {
	res, err := uc.send(ctx, p, streamID, in)

	switch {
	case err != nil:
		uc.obs.Rejected(entity.CodeOf(err))
	case !res.Duplicate:
		uc.obs.MessageSent()
	}

	return res, err
}

func (uc *UseCase) send(ctx context.Context, p entity.Principal, streamID string, in entity.SendInput) (entity.SendResult, error) {
	if err := uc.precheck(p, in); err != nil {
		return entity.SendResult{}, err
	}

	if res, retried, err := uc.retried(ctx, p, streamID, in.ClientMsgID); err != nil || retried {
		return res, err
	}

	body, sc, st, err := uc.prepare(ctx, p, streamID, in.Body)
	if err != nil {
		return entity.SendResult{}, err
	}

	hash := bodyHash(body)
	if lerr := uc.limit(ctx, p, sc, st, body, hash); lerr != nil {
		return entity.SendResult{}, lerr
	}

	m, err := uc.store(ctx, p, sc, st, in.ClientMsgID, body)
	if err != nil {
		return entity.SendResult{}, err
	}

	if rerr := uc.r.Guard.RememberMessage(ctx, p.UserID, streamID, in.ClientMsgID, m.ID, hash); rerr != nil {
		uc.l.Warn("chat - remember %s: %v", m.ID, rerr)
	}

	if _, err = uc.pub.Publish(ctx, entity.ChatTopic(streamID), entity.EventMessage, m); err != nil {
		return entity.SendResult{}, fmt.Errorf("ChatUseCase - Send - publish: %w", err)
	}

	return entity.SendResult{Message: m}, nil
}

// retried answers a clientMsgId that was already accepted with the stored message id.
func (uc *UseCase) retried(ctx context.Context, p entity.Principal, streamID, clientMsgID string) (entity.SendResult, bool, error) {
	id, ok, err := uc.r.Guard.AcceptedMessage(ctx, p.UserID, clientMsgID)
	if err != nil {
		return entity.SendResult{}, false, fmt.Errorf("ChatUseCase - Send - dedupe: %w", err)
	}

	if !ok {
		return entity.SendResult{}, false, nil
	}

	return entity.SendResult{Message: entity.Message{ID: id, StreamID: streamID}, Duplicate: true}, true, nil
}

// prepare normalizes the body, loads the room and the caller's standing and applies the stateless rules.
func (uc *UseCase) prepare(ctx context.Context, p entity.Principal, streamID, raw string,
) (string, entity.SendContext, entity.Standing, error) {
	body, err := entity.NormalizeBody(raw, uc.cfg.MaxLines)
	if err != nil {
		return "", entity.SendContext{}, entity.Standing{}, err
	}

	sc, st, err := uc.load(ctx, p, streamID)
	if err != nil {
		return "", entity.SendContext{}, entity.Standing{}, err
	}

	if aerr := uc.admit(p, sc, st); aerr != nil {
		return "", entity.SendContext{}, entity.Standing{}, aerr
	}

	return body, sc, st, nil
}

func (uc *UseCase) precheck(p entity.Principal, in entity.SendInput) error {
	if uc.cfg.Disabled {
		return entity.ErrChatDisabled
	}

	if p.Guest() {
		return entity.ErrUnauthenticated
	}

	if _, err := uuid.Parse(in.ClientMsgID); err != nil {
		return entity.Reject(entity.ErrValidation, "clientMsgId", entity.DetailFormat)
	}

	return nil
}

// load reads the room and the caller's standing; a stream without a room is CHAT_CLOSED (BR02).
func (uc *UseCase) load(ctx context.Context, p entity.Principal, streamID string) (entity.SendContext, entity.Standing, error) {
	sc, err := uc.r.Rooms.SendContext(ctx, streamID, p.UserID)
	if errors.Is(err, entity.ErrNotFound) {
		return entity.SendContext{}, entity.Standing{}, entity.ErrRoomClosed
	}

	if err != nil {
		return entity.SendContext{}, entity.Standing{}, fmt.Errorf("ChatUseCase - context: %w", err)
	}

	st, err := uc.standing(ctx, p, sc)
	if err != nil {
		return entity.SendContext{}, entity.Standing{}, err
	}

	return sc, st, nil
}

func (uc *UseCase) standing(ctx context.Context, p entity.Principal, sc entity.SendContext) (entity.Standing, error) {
	st := entity.Standing{Streamer: sc.OwnerID != "" && sc.OwnerID == p.UserID, Admin: p.IsAdmin()}

	if sc.Role != nil {
		st.Moderator = *sc.Role == entity.ChannelRoleModerator
		st.VIP = *sc.Role == entity.ChannelRoleVIP
	}

	if p.Guest() {
		return st, nil
	}

	followedAt, tier, err := uc.r.Audience.Audience(ctx, sc.Room.ChannelID, p.UserID)
	if err != nil {
		return entity.Standing{}, fmt.Errorf("ChatUseCase - standing: %w", err)
	}

	st.SubTier = tier

	if followedAt != nil {
		m := int(uc.now().Sub(*followedAt).Minutes())
		st.FollowedMinutes = &m
	}

	return st, nil
}

// admit applies the rules that do not change any state (room, e-mail, moderation, modes); Snapshot
// uses it to tell the client whether it can send.
func (uc *UseCase) admit(p entity.Principal, sc entity.SendContext, st entity.Standing) error {
	switch {
	case uc.cfg.Disabled:
		return entity.ErrChatDisabled
	case p.Guest():
		return entity.ErrUnauthenticated
	case sc.Room.Status == entity.RoomReadOnly:
		return entity.ErrRoomReadOnly
	case sc.Room.Status != entity.RoomOpen:
		return entity.ErrRoomClosed
	case uc.cfg.RequireVerified && (sc.User == nil || !sc.User.EmailVerified):
		return entity.ErrEmailNotVerified
	}

	if err := uc.restricted(sc, st); err != nil {
		return err
	}

	return modes(sc.Settings, st)
}

func (uc *UseCase) restricted(sc entity.SendContext, st entity.Standing) error {
	if st.Streamer || st.Admin {
		return nil
	}

	if sc.Restriction.Banned {
		return entity.ErrBanned
	}

	if until := sc.Restriction.TimedOutUntil; until != nil && until.After(uc.now()) {
		return entity.Reject(entity.ErrTimedOut, "until", until.UTC().Format(time.RFC3339))
	}

	return nil
}

// modes applies Followers-only and Subs-only with the BR14 exemptions; every mode on must pass (BR12).
func modes(s entity.ChatSettings, st entity.Standing) error {
	if s.FollowersMin != nil && !st.Privileged() {
		required := strconv.Itoa(*s.FollowersMin)

		switch {
		case st.FollowedMinutes == nil:
			return entity.Reject(entity.ErrFollowersOnly, "requiredMin", required)
		case *st.FollowedMinutes < *s.FollowersMin:
			return entity.Reject(entity.ErrFollowersOnly, "requiredMin", required, "followedMin", strconv.Itoa(*st.FollowedMinutes))
		}
	}

	if s.SubsOnly && !st.SkipsSubsOnly() {
		return entity.ErrSubsOnly
	}

	return nil
}

// limit applies the rules that consume state, cheapest refusal first: duplicate body, rate limits per
// user and per IP, auto-mod (a blocked message still costs its rate token, BR-F12), then slow mode (its
// marker is set last so a refused message does not start it).
func (uc *UseCase) limit(ctx context.Context, p entity.Principal, sc entity.SendContext, st entity.Standing, body, hash string) error {
	last, err := uc.r.Guard.LastBodyHash(ctx, p.UserID, sc.Room.StreamID)
	if err != nil {
		return fmt.Errorf("ChatUseCase - limit - duplicate: %w", err)
	}

	if last == hash {
		return entity.ErrDuplicate
	}

	if rerr := uc.rates(ctx, p, st); rerr != nil {
		return rerr
	}

	if aerr := uc.autoMod(ctx, sc, st, body); aerr != nil {
		return aerr
	}

	return uc.slow(ctx, p, sc, st)
}

// autoMod applies the channel's blocked terms and link blocking (BR-F09, BR-F10).
func (uc *UseCase) autoMod(ctx context.Context, sc entity.SendContext, st entity.Standing, body string) error {
	rules := entity.AutoModRules{BlockLinks: sc.Settings.BlockLinks}

	if uc.automod != nil && !st.Privileged() {
		terms, err := uc.automod.BlockedTerms(ctx, sc.Room.ChannelID)
		if err != nil {
			return fmt.Errorf("ChatUseCase - automod: %w", err)
		}

		rules.Terms = terms
	}

	return rules.Check(body, st)
}

func (uc *UseCase) rates(ctx context.Context, p entity.Principal, st entity.Standing) error {
	capacity := uc.cfg.RateLimit
	if st.Privileged() {
		capacity = uc.cfg.RateLimitPrivileged
	}

	if err := uc.take(ctx, _rateKeyUser+p.UserID, capacity); err != nil {
		return err
	}

	if p.IP == "" {
		return nil
	}

	return uc.take(ctx, _rateKeyIP+p.IP, uc.cfg.RateLimitIP)
}

func (uc *UseCase) slow(ctx context.Context, p entity.Principal, sc entity.SendContext, st entity.Standing) error {
	if sc.Settings.SlowSec == 0 || st.SkipsSlow() {
		return nil
	}

	ok, left, err := uc.r.Guard.StartSlow(ctx, sc.Room.StreamID, p.UserID, time.Duration(sc.Settings.SlowSec)*time.Second)
	if err != nil {
		return fmt.Errorf("ChatUseCase - slow: %w", err)
	}

	if !ok {
		return entity.Reject(entity.ErrSlowMode, "retryAfterSec", seconds(left))
	}

	return nil
}

func (uc *UseCase) take(ctx context.Context, key string, capacity int) error {
	ok, retry, err := uc.r.Guard.Take(ctx, key, capacity, uc.cfg.RateWindow)
	if err != nil {
		return fmt.Errorf("ChatUseCase - rate: %w", err)
	}

	if !ok {
		return entity.Reject(entity.ErrRateLimited, "retryAfterSec", seconds(retry))
	}

	return nil
}

// store builds the fragments and inserts the message.
func (uc *UseCase) store(ctx context.Context, p entity.Principal, sc entity.SendContext, st entity.Standing,
	clientMsgID, body string,
) (entity.Message, error) {
	users := map[string]string{}

	if names := entity.MentionCandidates(body); len(names) > 0 {
		var err error
		if users, err = uc.r.Users.UserIDsByUsername(ctx, names); err != nil {
			return entity.Message{}, fmt.Errorf("ChatUseCase - store - mentions: %w", err)
		}
	}

	emotes, err := uc.emoteCodes(ctx)
	if err != nil {
		return entity.Message{}, err
	}

	m, err := uc.r.Messages.InsertMessage(ctx, entity.Message{
		StreamID:    sc.Room.StreamID,
		User:        author(p.UserID, sc.User, st),
		Body:        body,
		Fragments:   entity.BuildFragments(body, emotes, users),
		Type:        entity.MessageNormal,
		ClientMsgID: clientMsgID,
	})
	if err != nil {
		return entity.Message{}, fmt.Errorf("ChatUseCase - store: %w", err)
	}

	return m, nil
}

// author falls back to a short id when the identity copy has no row for the user yet.
func author(userID string, u *entity.User, st entity.Standing) entity.Author {
	a := entity.Author{ID: userID, Color: entity.ColorOf(userID), Badges: st.Badges()}

	if u != nil {
		a.Username, a.DisplayName = u.Username, u.Name()
	} else {
		short := userID
		if len(short) > _fallbackIDRune {
			short = short[:_fallbackIDRune]
		}

		a.Username, a.DisplayName = "user-"+short, "user-"+short
	}

	return a
}

func bodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))

	return hex.EncodeToString(sum[:_bodyHashBytes])
}

// seconds rounds up so a client never retries a moment too early.
func seconds(d time.Duration) string {
	return strconv.Itoa(int(math.Ceil(d.Seconds())))
}
