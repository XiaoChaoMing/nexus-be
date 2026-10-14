// Package search implements suggestions, the results page and the search history (NXL-C §8.5).
package search

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/channel/internal/usecase/enrich"
)

// Paging of GET /v1/search (NXL-00 §6).
const (
	DefaultLimit = 24
	MaxLimit     = 50
)

// UseCase implements usecase.Search.
type UseCase struct {
	search   repo.SearchRepo
	channels repo.ChannelRepo
	enrich   *enrich.Enricher
	l        logger.Interface
}

var _ usecase.Search = (*UseCase)(nil)

// New -.
func New(s repo.SearchRepo, ch repo.ChannelRepo, e *enrich.Enricher, l logger.Interface) *UseCase {
	return &UseCase{search: s, channels: ch, enrich: e, l: l}
}

func normalized(field, q string) (string, error) {
	q, ok := entity.NormalizeQuery(q)
	if !ok {
		return "", entity.Reject(entity.ErrValidation, field, entity.DetailLength)
	}

	return q, nil
}

// Suggest returns the non-empty groups in display order (channel, stream, category, vod).
func (uc *UseCase) Suggest(ctx context.Context, q string, types []entity.DocType, limit int) ([]entity.SearchGroup, error) {
	q, err := normalized("q", q)
	if err != nil {
		return nil, err
	}

	if len(types) == 0 {
		types = entity.DocTypes()
	}

	if limit <= 0 || limit > entity.SearchLimitMax {
		limit = entity.SearchGroupMax
	}

	docs, err := uc.search.Suggest(ctx, q, types, limit)
	if err != nil {
		return nil, fmt.Errorf("SearchUseCase - Suggest: %w", err)
	}

	return group(types, docs), nil
}

// group returns the non-empty groups of docs in display order.
func group(types []entity.DocType, docs []entity.SearchDoc) []entity.SearchGroup {
	out := []entity.SearchGroup{}

	for _, t := range entity.DocTypes() {
		if !slices.Contains(types, t) {
			continue
		}

		g := entity.SearchGroup{Type: t}

		for _, d := range docs {
			if d.Type == t {
				g.Items = append(g.Items, d)
			}
		}

		if len(g.Items) > 0 {
			out = append(out, g)
		}
	}

	return out
}

// Search -.
func (uc *UseCase) Search(ctx context.Context, q string, t entity.DocType, cursor string, limit int) (usecase.SearchPage, error) {
	q, err := normalized("q", q)
	if err != nil {
		return usecase.SearchPage{}, err
	}

	if limit <= 0 || limit > MaxLimit {
		limit = DefaultLimit
	}

	var after *repo.SearchCursor

	if cursor != "" {
		c, cerr := decodeCursor(cursor)
		if cerr != nil {
			return usecase.SearchPage{}, cerr
		}

		after = &c
	}

	docs, err := uc.search.Search(ctx, q, t, after, limit+1)
	if err != nil {
		return usecase.SearchPage{}, fmt.Errorf("SearchUseCase - Search: %w", err)
	}

	page := usecase.SearchPage{Items: docs}

	if len(docs) > limit {
		page.Items = docs[:limit]
		page.NextCursor = encodeCursor(page.Items[limit-1])
	}

	return page, nil
}

// History -.
func (uc *UseCase) History(ctx context.Context, userID string) ([]entity.SearchQuery, error) {
	out, err := uc.search.History(ctx, userID, entity.SearchHistoryMax)
	if err != nil {
		return nil, fmt.Errorf("SearchUseCase - History: %w", err)
	}

	return out, nil
}

// Remember -.
func (uc *UseCase) Remember(ctx context.Context, userID, query string) (entity.SearchQuery, error) {
	query, err := normalized("query", query)
	if err != nil {
		return entity.SearchQuery{}, err
	}

	out, err := uc.search.Remember(ctx, userID, query, entity.SearchHistoryMax)
	if err != nil {
		return entity.SearchQuery{}, fmt.Errorf("SearchUseCase - Remember: %w", err)
	}

	return out, nil
}

// Forget -.
func (uc *UseCase) Forget(ctx context.Context, userID string, id *string) error {
	if err := uc.search.Forget(ctx, userID, id); err != nil {
		return fmt.Errorf("SearchUseCase - Forget: %w", err)
	}

	return nil
}

// RefreshViewers copies the live viewer counts into the search documents (ranking, viewerCount).
func (uc *UseCase) RefreshViewers(ctx context.Context) error {
	if !uc.enrich.Available() {
		return nil
	}

	ids, err := uc.channels.LiveStreamIDs(ctx)
	if err != nil {
		return fmt.Errorf("SearchUseCase - RefreshViewers: %w", err)
	}

	counts := uc.enrich.Counts(ctx, ids)
	if counts == nil {
		return nil
	}

	if err = uc.search.SetViewers(ctx, counts); err != nil {
		return fmt.Errorf("SearchUseCase - RefreshViewers: %w", err)
	}

	return nil
}

type cursor struct {
	PrefixHit bool   `json:"p"`
	IsLive    bool   `json:"l"`
	Rank      int    `json:"r"`
	Viewers   int    `json:"v"`
	ID        string `json:"i"`
}

func encodeCursor(d entity.SearchDoc) string {
	b, err := json.Marshal(cursor{PrefixHit: d.PrefixHit, IsLive: d.IsLive, Rank: d.Rank, Viewers: d.Viewers, ID: d.ID})
	if err != nil {
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (repo.SearchCursor, error) {
	bad := entity.Reject(entity.ErrValidation, "cursor", entity.DetailInvalid)

	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return repo.SearchCursor{}, bad
	}

	var c cursor
	if err = json.Unmarshal(b, &c); err != nil {
		return repo.SearchCursor{}, bad
	}

	if _, err = uuid.Parse(c.ID); err != nil {
		return repo.SearchCursor{}, bad
	}

	return repo.SearchCursor(c), nil
}
