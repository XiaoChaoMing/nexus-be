// Package streams implements stream keys, the stream lifecycle, the public catalog and the creator → channel map.
package streams

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

const (
	_keyPrefix = "live_"
	_keyBytes  = 32
	// A health report older than this is not shown (the stream is probably interrupted).
	_healthTTL = 20 * time.Second
	_maxLimit  = 50
)

// Authorize results reported to the Observer.
const (
	AuthorizeOK            = "ok"
	AuthorizeResumed       = "resumed"
	AuthorizeInvalidKey    = "invalid_key"
	AuthorizeAlreadyLive   = "already_live"
	AuthorizeRecentlyEnded = "recently_ended"
	AuthorizeError         = "error"
)

// UseCase implements usecase.Studio, usecase.Ingest, usecase.Catalog and usecase.ChannelSync.
type UseCase struct {
	keys      repo.KeyRepo
	settings  repo.SettingsRepo
	streams   repo.StreamRepo
	channels  repo.ChannelRepo
	viewers   repo.ViewerCountRepo
	grace     time.Duration
	retention time.Duration
	cooldown  time.Duration
	obs       usecase.Observer
	now       func() time.Time
	l         logger.Interface

	healthMu sync.Mutex
	health   map[string]entity.Health
}

var (
	_ usecase.Studio      = (*UseCase)(nil)
	_ usecase.Ingest      = (*UseCase)(nil)
	_ usecase.Catalog     = (*UseCase)(nil)
	_ usecase.ChannelSync = (*UseCase)(nil)
)

// Option -.
type Option func(*UseCase)

// EndCooldown refuses publishes for d after the creator ends a stream (OBS auto-reconnect).
func EndCooldown(d time.Duration) Option {
	return func(uc *UseCase) { uc.cooldown = d }
}

// WithObserver -.
func WithObserver(o usecase.Observer) Option {
	return func(uc *UseCase) { uc.obs = o }
}

// WithViewerCounts adds viewerCount to the catalog from chat-svc's counts.
func WithViewerCounts(v repo.ViewerCountRepo) Option {
	return func(uc *UseCase) { uc.viewers = v }
}

// WithClock -.
func WithClock(now func() time.Time) Option {
	return func(uc *UseCase) { uc.now = now }
}

// New -.
func New(k repo.KeyRepo, s repo.SettingsRepo, st repo.StreamRepo, ch repo.ChannelRepo, grace, retention time.Duration,
	l logger.Interface, opts ...Option,
) *UseCase {
	uc := &UseCase{
		keys: k, settings: s, streams: st, channels: ch, grace: grace, retention: retention, l: l,
		obs: noopObserver{}, now: time.Now,
		health: map[string]entity.Health{},
	}

	for _, o := range opts {
		o(uc)
	}

	return uc
}

type noopObserver struct{}

func (noopObserver) Authorize(string)         {}
func (noopObserver) Transition(entity.Status) {}
func (noopObserver) OnAir(int)                {}

// HashKey is how keys are stored and looked up: never the key itself.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))

	return hex.EncodeToString(sum[:])
}

// ---- Studio --------------------------------------------------------------------------------------

// Channel -.
func (uc *UseCase) Channel(ctx context.Context, ownerID string) (entity.Channel, error) {
	ch, err := uc.channels.ChannelOfOwner(ctx, ownerID)
	if errors.Is(err, entity.ErrNotFound) {
		return entity.Channel{}, entity.ErrNotCreator
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("StreamUseCase - Channel: %w", err)
	}

	return ch, nil
}

// CreateKey returns the new key once; the previous key stops working immediately.
func (uc *UseCase) CreateKey(ctx context.Context, channelID string) (string, error) {
	b := make([]byte, _keyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("StreamUseCase - CreateKey - rand: %w", err)
	}

	key := _keyPrefix + base64.RawURLEncoding.EncodeToString(b)

	if _, err := uc.keys.Replace(ctx, channelID, HashKey(key)); err != nil {
		return "", fmt.Errorf("StreamUseCase - CreateKey: %w", err)
	}

	return key, nil
}

// KeyInfo -.
func (uc *UseCase) KeyInfo(ctx context.Context, channelID string) (entity.StreamKeyInfo, error) {
	return uc.keys.Info(ctx, channelID)
}

// Overview -.
func (uc *UseCase) Overview(ctx context.Context, channelID string) (usecase.StudioOverview, error) {
	set, err := uc.settings.GetSettings(ctx, channelID)
	if err != nil {
		return usecase.StudioOverview{}, fmt.Errorf("StreamUseCase - Overview - settings: %w", err)
	}

	cur, err := uc.streams.Current(ctx, channelID)
	if err != nil {
		return usecase.StudioOverview{}, fmt.Errorf("StreamUseCase - Overview - current: %w", err)
	}

	out := usecase.StudioOverview{Settings: set, Current: cur}
	if cur != nil {
		out.Health = uc.healthOf(cur.ID)
	}

	return out, nil
}

// UpdateSettings normalises tags (trimmed, de-duplicated) before saving.
func (uc *UseCase) UpdateSettings(ctx context.Context, s entity.Settings) (entity.Settings, error) {
	s.Title = strings.TrimSpace(s.Title)

	seen := map[string]bool{}
	tags := make([]string, 0, len(s.Tags))

	for _, t := range s.Tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}

		seen[strings.ToLower(t)] = true
		tags = append(tags, t)
	}

	s.Tags = tags

	if s.Language == "" {
		s.Language = "vi"
	}

	if err := uc.settings.SaveSettings(ctx, s); err != nil {
		return entity.Settings{}, fmt.Errorf("StreamUseCase - UpdateSettings: %w", err)
	}

	return uc.settings.GetSettings(ctx, s.ChannelID)
}

// End -.
func (uc *UseCase) End(ctx context.Context, channelID string) error {
	s, err := uc.streams.End(ctx, channelID, entity.EndCreator)
	if err != nil {
		if errors.Is(err, entity.ErrNotLive) {
			return err
		}

		return fmt.Errorf("StreamUseCase - End: %w", err)
	}

	uc.dropHealth(s.ID)
	uc.obs.Transition(entity.StatusEnded)
	uc.l.Info("stream %s ended by creator", s.ID)

	return nil
}

// ---- Ingest --------------------------------------------------------------------------------------

// Authorize -.
func (uc *UseCase) Authorize(ctx context.Context, streamKey, clientIP string) (entity.Grant, error) {
	g, err := uc.authorize(ctx, streamKey, clientIP)
	uc.obs.Authorize(authorizeResult(g, err))

	if err == nil {
		uc.obs.Transition(entity.StatusLive)
	}

	return g, err
}

func (uc *UseCase) authorize(ctx context.Context, streamKey, clientIP string) (entity.Grant, error) {
	if !strings.HasPrefix(streamKey, _keyPrefix) {
		return entity.Grant{}, entity.ErrInvalidStreamKey
	}

	channelID, err := uc.keys.ChannelByKeyHash(ctx, HashKey(streamKey))
	if err != nil {
		if errors.Is(err, entity.ErrInvalidStreamKey) {
			uc.l.Warn("authorize: invalid stream key from %s", clientIP)
		}

		return entity.Grant{}, err
	}

	if uc.cooldown > 0 {
		recent, cerr := uc.streams.EndedByCreatorSince(ctx, channelID, uc.now().Add(-uc.cooldown))
		if cerr != nil {
			return entity.Grant{}, fmt.Errorf("StreamUseCase - Authorize - cooldown: %w", cerr)
		}

		if recent {
			uc.l.Info("authorize: channel %s ended moments ago, refusing publish from %s", channelID, clientIP)

			return entity.Grant{}, entity.ErrRecentlyEnded
		}
	}

	g, err := uc.streams.Authorize(ctx, channelID, uc.grace)
	if err != nil {
		return entity.Grant{}, err
	}

	uc.l.Info("authorize: channel %s → stream %s (resumed=%t) from %s", channelID, g.StreamID, g.Resumed, clientIP)

	return g, nil
}

func authorizeResult(g entity.Grant, err error) string {
	switch {
	case err == nil && g.Resumed:
		return AuthorizeResumed
	case err == nil:
		return AuthorizeOK
	case errors.Is(err, entity.ErrInvalidStreamKey):
		return AuthorizeInvalidKey
	case errors.Is(err, entity.ErrAlreadyLive):
		return AuthorizeAlreadyLive
	case errors.Is(err, entity.ErrRecentlyEnded):
		return AuthorizeRecentlyEnded
	default:
		return AuthorizeError
	}
}

// IngestStarted -.
func (uc *UseCase) IngestStarted(ctx context.Context, eventID, streamID string) error {
	return uc.streams.OnIngestStarted(ctx, eventID, streamID)
}

// IngestStopped -.
func (uc *UseCase) IngestStopped(ctx context.Context, eventID, streamID string) error {
	uc.dropHealth(streamID)

	if err := uc.streams.OnIngestStopped(ctx, eventID, streamID); err != nil {
		return err
	}

	// The update is conditional (a stream ended by its creator is kicked too): count real transitions only.
	if s, err := uc.streams.Get(ctx, streamID); err == nil && s.Status == entity.StatusInterrupted {
		uc.obs.Transition(entity.StatusInterrupted)
	}

	return nil
}

// IngestHealth -.
func (uc *UseCase) IngestHealth(streamID string, h entity.Health) {
	uc.healthMu.Lock()
	defer uc.healthMu.Unlock()

	uc.health[streamID] = h
}

// VODProcessed -.
func (uc *UseCase) VODProcessed(ctx context.Context, eventID string, v repo.VODInput) error {
	uc.dropHealth(v.StreamID)

	v.Retention = uc.retention

	return uc.streams.OnVODProcessed(ctx, eventID, v)
}

// PeakViewers -.
func (uc *UseCase) PeakViewers(ctx context.Context, eventID, streamID string, peak int) error {
	if err := uc.streams.SetPeakViewers(ctx, eventID, streamID, peak); err != nil {
		return fmt.Errorf("StreamUseCase - PeakViewers: %w", err)
	}

	return nil
}

// ExpireInterrupted -.
func (uc *UseCase) ExpireInterrupted(ctx context.Context) (int, error) {
	ended, err := uc.streams.ExpireInterrupted(ctx, uc.grace)
	if err != nil {
		return 0, fmt.Errorf("StreamUseCase - ExpireInterrupted: %w", err)
	}

	for _, s := range ended {
		uc.dropHealth(s.ID)
		uc.obs.Transition(entity.StatusEnded)
		uc.l.Info("stream %s ended: no reconnect within %s", s.ID, uc.grace)
	}

	if n, cerr := uc.streams.CountOnAir(ctx); cerr == nil {
		uc.obs.OnAir(n)
	} else {
		uc.l.Warn("stream - count on air: %v", cerr)
	}

	return len(ended), nil
}

// ExpireVODs -.
func (uc *UseCase) ExpireVODs(ctx context.Context) (int64, error) {
	n, err := uc.streams.ExpireVODs(ctx)
	if err != nil {
		return 0, fmt.Errorf("StreamUseCase - ExpireVODs: %w", err)
	}

	if n > 0 {
		uc.l.Info("expired %d VODs past retention", n)
	}

	return n, nil
}

func (uc *UseCase) healthOf(streamID string) *entity.Health {
	uc.healthMu.Lock()
	defer uc.healthMu.Unlock()

	h, ok := uc.health[streamID]
	if !ok || time.Since(h.ReportedAt) > _healthTTL {
		return nil
	}

	return &h
}

func (uc *UseCase) dropHealth(streamID string) {
	uc.healthMu.Lock()
	defer uc.healthMu.Unlock()

	delete(uc.health, streamID)
}

// ---- Catalog -------------------------------------------------------------------------------------

// ErrBadCursor -.
var ErrBadCursor = errors.New("invalid cursor")

// ListLive -.
func (uc *UseCase) ListLive(ctx context.Context, cursor string, limit int) (usecase.Page, error) {
	if limit <= 0 || limit > _maxLimit {
		limit = 24
	}

	var after *repo.Cursor

	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return usecase.Page{}, err
		}

		after = &c
	}

	items, err := uc.streams.ListOnAir(ctx, after, limit+1)
	if err != nil {
		return usecase.Page{}, fmt.Errorf("StreamUseCase - ListLive: %w", err)
	}

	page := usecase.Page{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]

		if last.StartedAt != nil {
			page.NextCursor = encodeCursor(repo.Cursor{StartedAt: *last.StartedAt, ID: last.ID})
		}
	}

	if err = uc.attachChannels(ctx, page.Items); err != nil {
		return usecase.Page{}, fmt.Errorf("StreamUseCase - ListLive: %w", err)
	}

	uc.attachViewerCounts(ctx, page.Items)

	return page, nil
}

// Get -.
func (uc *UseCase) Get(ctx context.Context, streamID string) (entity.Stream, error) {
	s, err := uc.streams.Get(ctx, streamID)
	if err != nil {
		return entity.Stream{}, err
	}

	items := []entity.Stream{s}
	if err = uc.attachChannels(ctx, items); err != nil {
		return entity.Stream{}, fmt.Errorf("StreamUseCase - Get: %w", err)
	}

	uc.attachViewerCounts(ctx, items)

	return items[0], nil
}

// GetVOD -.
func (uc *UseCase) GetVOD(ctx context.Context, vodID string) (entity.VOD, error) {
	v, err := uc.streams.GetVOD(ctx, vodID)
	if err != nil {
		return entity.VOD{}, err
	}

	byID, err := uc.channels.ChannelsByID(ctx, []string{v.ChannelID})
	if err != nil {
		return entity.VOD{}, fmt.Errorf("StreamUseCase - GetVOD - channel: %w", err)
	}

	if ch, ok := byID[v.ChannelID]; ok {
		v.Channel = &ch
	}

	return v, nil
}

// attachChannels fills Stream.Channel with one batched lookup for the whole page.
func (uc *UseCase) attachChannels(ctx context.Context, items []entity.Stream) error {
	if len(items) == 0 {
		return nil
	}

	ids := make([]string, 0, len(items))
	for _, s := range items {
		ids = append(ids, s.ChannelID)
	}

	byID, err := uc.channels.ChannelsByID(ctx, ids)
	if err != nil {
		return fmt.Errorf("channels: %w", err)
	}

	for i := range items {
		if ch, ok := byID[items[i].ChannelID]; ok {
			items[i].Channel = &ch
		}
	}

	return nil
}

// attachViewerCounts fills Stream.ViewerCount for streams on air. Counts are decoration: when Redis is
// unavailable the catalog still answers, with viewerCount null.
func (uc *UseCase) attachViewerCounts(ctx context.Context, items []entity.Stream) {
	if uc.viewers == nil {
		return
	}

	ids := make([]string, 0, len(items))

	for _, s := range items {
		if s.OnAir() {
			ids = append(ids, s.ID)
		}
	}

	if len(ids) == 0 {
		return
	}

	counts, err := uc.viewers.ViewerCounts(ctx, ids)
	if err != nil {
		uc.l.Warn("stream - viewer counts: %v", err)

		return
	}

	for i := range items {
		if n, ok := counts[items[i].ID]; ok {
			items[i].ViewerCount = &n
		}
	}
}

// ---- Channels ------------------------------------------------------------------------------------

// ChannelCreated -.
func (uc *UseCase) ChannelCreated(ctx context.Context, eventID string, c entity.Channel) error {
	if err := uc.channels.UpsertChannel(ctx, eventID, c); err != nil {
		return fmt.Errorf("StreamUseCase - ChannelCreated: %w", err)
	}

	uc.l.Info("channel %s (%s) mapped to creator %s", c.ID, c.Handle, c.OwnerID)

	return nil
}

func encodeCursor(c repo.Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.StartedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID))
}

func decodeCursor(s string) (repo.Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return repo.Cursor{}, ErrBadCursor
	}

	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return repo.Cursor{}, ErrBadCursor
	}

	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return repo.Cursor{}, ErrBadCursor
	}

	if _, err = uuid.Parse(id); err != nil {
		return repo.Cursor{}, ErrBadCursor
	}

	return repo.Cursor{StartedAt: t, ID: id}, nil
}
