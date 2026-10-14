package streams

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/stream/internal/entity"
	"github.com/minhnn/nexuslive-be/services/stream/internal/repo"
	"github.com/minhnn/nexuslive-be/services/stream/internal/usecase"
)

const (
	// _onAirMax bounds the in-memory sort by viewers; streams on air never come close (MEDIA_MAX_CONCURRENT_STREAMS).
	_onAirMax = 500
	// RecommendedMax is the largest page of the recommendation endpoints.
	RecommendedMax = 24
	// DefaultRecommended -.
	DefaultRecommended = 12
)

// List sorts by viewers in memory (counts live in Redis) with a cursor on (viewers, startedAt, id); newest uses
// the SQL keyset. A count that changes between two pages can repeat or skip a card; the web client de-duplicates.
func (uc *UseCase) List(ctx context.Context, f usecase.StreamFilter, cursor string, limit int) (usecase.Page, error) {
	if limit <= 0 || limit > _maxLimit {
		limit = 24
	}

	if f.Sort == usecase.SortNewest {
		return uc.listNewest(ctx, f.StreamFilter, cursor, limit)
	}

	var after *viewerCursor

	if cursor != "" {
		c, err := decodeViewerCursor(cursor)
		if err != nil {
			return usecase.Page{}, err
		}

		after = &c
	}

	all, err := uc.streams.ListOnAirAll(ctx, f.StreamFilter, _onAirMax)
	if err != nil {
		return usecase.Page{}, fmt.Errorf("StreamUseCase - List: %w", err)
	}

	uc.attachViewerCounts(ctx, all)
	sortByViewers(all)

	items := after.rest(all)
	page := usecase.Page{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeViewerCursor(page.Items[limit-1])
	}

	if err = uc.attachChannels(ctx, page.Items); err != nil {
		return usecase.Page{}, fmt.Errorf("StreamUseCase - List: %w", err)
	}

	return page, nil
}

func (uc *UseCase) listNewest(ctx context.Context, f repo.StreamFilter, cursor string, limit int) (usecase.Page, error) {
	var after *repo.Cursor

	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return usecase.Page{}, err
		}

		after = &c
	}

	items, err := uc.streams.ListOnAirFiltered(ctx, f, after, limit+1)
	if err != nil {
		return usecase.Page{}, fmt.Errorf("StreamUseCase - List: %w", err)
	}

	page := usecase.Page{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]

		if last := page.Items[limit-1]; last.StartedAt != nil {
			page.NextCursor = encodeCursor(repo.Cursor{StartedAt: *last.StartedAt, ID: last.ID})
		}
	}

	if err = uc.attachChannels(ctx, page.Items); err != nil {
		return usecase.Page{}, fmt.Errorf("StreamUseCase - List: %w", err)
	}

	uc.attachViewerCounts(ctx, page.Items)

	return page, nil
}

func viewersOf(s entity.Stream) int {
	if s.ViewerCount == nil {
		return 0
	}

	return *s.ViewerCount
}

func startedOf(s entity.Stream) time.Time {
	if s.StartedAt == nil {
		return time.Time{}
	}

	return *s.StartedAt
}

// sortByViewers orders by viewers, then the most recent start, then id (all descending).
func sortByViewers(items []entity.Stream) {
	sort.SliceStable(items, func(i, j int) bool {
		return viewerCursorOf(items[i]).before(items[j])
	})
}

type viewerCursor struct {
	Viewers   int    `json:"v"`
	StartedAt int64  `json:"s"`
	ID        string `json:"id"`
}

func viewerCursorOf(s entity.Stream) viewerCursor {
	return viewerCursor{Viewers: viewersOf(s), StartedAt: startedOf(s).UnixMicro(), ID: s.ID}
}

// before reports whether s comes after the cursor in (viewers, startedAt, id) descending order.
func (c viewerCursor) before(s entity.Stream) bool {
	k := viewerCursorOf(s)

	switch {
	case k.Viewers != c.Viewers:
		return k.Viewers < c.Viewers
	case k.StartedAt != c.StartedAt:
		return k.StartedAt < c.StartedAt
	}

	return k.ID < c.ID
}

// rest returns the streams of sorted after the cursor (all of them for a nil cursor).
func (c *viewerCursor) rest(sorted []entity.Stream) []entity.Stream {
	if c == nil {
		return sorted
	}

	out := []entity.Stream{}

	for _, s := range sorted {
		if c.before(s) {
			out = append(out, s)
		}
	}

	return out
}

func encodeViewerCursor(s entity.Stream) string {
	b, err := json.Marshal(viewerCursorOf(s))
	if err != nil {
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeViewerCursor(s string) (viewerCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return viewerCursor{}, ErrBadCursor
	}

	var c viewerCursor
	if err = json.Unmarshal(b, &c); err != nil {
		return viewerCursor{}, ErrBadCursor
	}

	if _, err = uuid.Parse(c.ID); err != nil {
		return viewerCursor{}, ErrBadCursor
	}

	return c, nil
}

// Recommended -.
func (uc *UseCase) Recommended(ctx context.Context, limit int) ([]entity.Stream, error) {
	if limit <= 0 || limit > RecommendedMax {
		limit = DefaultRecommended
	}

	all, err := uc.streams.ListOnAirAll(ctx, repo.StreamFilter{}, _onAirMax)
	if err != nil {
		return nil, fmt.Errorf("StreamUseCase - Recommended: %w", err)
	}

	// INTERRUPTED streams are not recommended (NXL-C BR03), nor is 18+ content (G4-Q4).
	out := []entity.Stream{}

	for _, s := range all {
		if s.Status == entity.StatusLive && !s.IsMature {
			out = append(out, s)
		}
	}

	uc.attachViewerCounts(ctx, out)
	sortByViewers(out)

	if len(out) > limit {
		out = out[:limit]
	}

	if err = uc.attachChannels(ctx, out); err != nil {
		return nil, fmt.Errorf("StreamUseCase - Recommended: %w", err)
	}

	return out, nil
}

// owns reports whether viewerID owns channelID (guests own nothing).
func (uc *UseCase) owns(ctx context.Context, viewerID, channelID string) (bool, error) {
	if viewerID == "" {
		return false, nil
	}

	ch, err := uc.channels.ChannelOfOwner(ctx, viewerID)
	if errors.Is(err, entity.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("owner: %w", err)
	}

	return ch.ID == channelID, nil
}

// GetFor hides the VOD of the stream when it is hidden and the caller is not the owner.
func (uc *UseCase) GetFor(ctx context.Context, streamID, viewerID string) (entity.Stream, error) {
	s, err := uc.Get(ctx, streamID)
	if err != nil {
		return entity.Stream{}, err
	}

	if s.VODVisibility != nil && *s.VODVisibility == entity.VisibilityHidden {
		owner, oerr := uc.owns(ctx, viewerID, s.ChannelID)
		if oerr != nil {
			return entity.Stream{}, fmt.Errorf("StreamUseCase - GetFor: %w", oerr)
		}

		if !owner {
			s.VODID, s.VODVisibility = nil, nil
		}
	}

	return s, nil
}

// GetVODFor returns entity.ErrNotFound for a hidden VOD unless the caller owns it.
func (uc *UseCase) GetVODFor(ctx context.Context, vodID, viewerID string) (entity.VOD, error) {
	v, err := uc.GetVOD(ctx, vodID)
	if err != nil {
		return entity.VOD{}, err
	}

	if v.Hidden() {
		owner, oerr := uc.owns(ctx, viewerID, v.ChannelID)
		if oerr != nil {
			return entity.VOD{}, fmt.Errorf("StreamUseCase - GetVODFor: %w", oerr)
		}

		if !owner {
			return entity.VOD{}, entity.ErrNotFound
		}
	}

	return v, nil
}

// VODs -.
func (uc *UseCase) VODs(ctx context.Context, f repo.VODFilter, cursor string, limit int) (usecase.VODPage, error) {
	return uc.vodPage(ctx, "VODs", cursor, limit, func(after *repo.VODCursor, n int) ([]entity.VOD, error) {
		return uc.vods.ListPublic(ctx, f, false, after, n)
	})
}

// RecommendedVODs are the newest public VODs that are not 18+.
func (uc *UseCase) RecommendedVODs(ctx context.Context, limit int) ([]entity.VOD, error) {
	if limit <= 0 || limit > RecommendedMax {
		limit = DefaultRecommended
	}

	items, err := uc.vods.ListPublic(ctx, repo.VODFilter{}, true, nil, limit)
	if err != nil {
		return nil, fmt.Errorf("StreamUseCase - RecommendedVODs: %w", err)
	}

	if err = uc.attachVODChannels(ctx, items); err != nil {
		return nil, fmt.Errorf("StreamUseCase - RecommendedVODs: %w", err)
	}

	return items, nil
}

// ChannelVODs -.
func (uc *UseCase) ChannelVODs(ctx context.Context, channelID, cursor string, limit int) (usecase.VODPage, error) {
	known, err := uc.channels.ChannelsByID(ctx, []string{channelID})
	if err != nil {
		return usecase.VODPage{}, fmt.Errorf("StreamUseCase - ChannelVODs: %w", err)
	}

	if _, ok := known[channelID]; !ok {
		return usecase.VODPage{}, entity.ErrNotFound
	}

	return uc.vodPage(ctx, "ChannelVODs", cursor, limit, func(after *repo.VODCursor, n int) ([]entity.VOD, error) {
		return uc.vods.ListByChannel(ctx, channelID, true, repo.StudioVODFilter{}, after, n)
	})
}

// vodPage pages a VOD list (created_at DESC, id DESC) and attaches the channels.
func (uc *UseCase) vodPage(ctx context.Context, op, cursor string, limit int,
	list func(after *repo.VODCursor, n int) ([]entity.VOD, error),
) (usecase.VODPage, error) {
	if limit <= 0 || limit > _maxLimit {
		limit = 24
	}

	var after *repo.VODCursor

	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return usecase.VODPage{}, err
		}

		after = &repo.VODCursor{CreatedAt: c.StartedAt, ID: c.ID}
	}

	items, err := list(after, limit+1)
	if err != nil {
		return usecase.VODPage{}, fmt.Errorf("StreamUseCase - %s: %w", op, err)
	}

	page := usecase.VODPage{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(repo.Cursor{StartedAt: last.CreatedAt, ID: last.ID})
	}

	if err = uc.attachVODChannels(ctx, page.Items); err != nil {
		return usecase.VODPage{}, fmt.Errorf("StreamUseCase - %s: %w", op, err)
	}

	return page, nil
}

func (uc *UseCase) attachVODChannels(ctx context.Context, items []entity.VOD) error {
	return attach(ctx, uc.channels, items, func(v *entity.VOD) *string { return &v.ChannelID },
		func(v *entity.VOD, ch entity.Channel) { v.Channel = &ch })
}

// attach fills the channel of each item with one batched lookup.
func attach[T any](ctx context.Context, channels repo.ChannelRepo, items []T, idOf func(*T) *string,
	set func(*T, entity.Channel),
) error {
	if len(items) == 0 {
		return nil
	}

	ids := make([]string, 0, len(items))
	for i := range items {
		ids = append(ids, *idOf(&items[i]))
	}

	byID, err := channels.ChannelsByID(ctx, ids)
	if err != nil {
		return fmt.Errorf("channels: %w", err)
	}

	for i := range items {
		if ch, ok := byID[*idOf(&items[i])]; ok {
			set(&items[i], ch)
		}
	}

	return nil
}
