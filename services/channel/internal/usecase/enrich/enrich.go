// Package enrich decorates streams on air with their category and live viewer count. Both are
// decoration: when a lookup fails the stream is returned without it.
package enrich

import (
	"context"

	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

// Enricher -.
type Enricher struct {
	categories repo.CategoryRepo
	// viewers is nil when Redis is not configured: counts stay null.
	viewers repo.ViewerCountRepo
	l       logger.Interface
}

// New -.
func New(categories repo.CategoryRepo, viewers repo.ViewerCountRepo, l logger.Interface) *Enricher {
	return &Enricher{categories: categories, viewers: viewers, l: l}
}

// Live fills Category and ViewerCount of each stream with one lookup of each kind.
func (e *Enricher) Live(ctx context.Context, streams []*entity.LiveStream) {
	if len(streams) == 0 {
		return
	}

	catIDs := make([]string, 0, len(streams))
	streamIDs := make([]string, 0, len(streams))

	for _, s := range streams {
		streamIDs = append(streamIDs, s.StreamID)

		if s.CategoryID != nil {
			catIDs = append(catIDs, *s.CategoryID)
		}
	}

	cats := e.Categories(ctx, catIDs)
	counts := e.Counts(ctx, streamIDs)

	for _, s := range streams {
		if s.CategoryID != nil {
			if c, ok := cats[*s.CategoryID]; ok {
				s.Category = &c
			}
		}

		if counts == nil {
			continue
		}

		n := counts[s.StreamID]
		s.ViewerCount = &n
	}
}

// Categories returns the known categories among ids (empty when the lookup fails).
func (e *Enricher) Categories(ctx context.Context, ids []string) map[string]entity.Category {
	cats, err := e.categories.ByIDs(ctx, ids)
	if err != nil {
		e.l.Warn("channel - categories: %v", err)

		return map[string]entity.Category{}
	}

	return cats
}

// Counts returns the viewer counts of streamIDs (missing = 0), or nil when counts are unavailable.
func (e *Enricher) Counts(ctx context.Context, streamIDs []string) map[string]int {
	if e.viewers == nil {
		return nil
	}

	counts, err := e.viewers.ViewerCounts(ctx, streamIDs)
	if err != nil {
		e.l.Warn("channel - viewer counts: %v", err)

		return nil
	}

	return counts
}

// Available reports whether viewer counts are configured.
func (e *Enricher) Available() bool { return e.viewers != nil }
