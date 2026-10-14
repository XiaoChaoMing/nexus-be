package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

var _ repo.CategoryRepo = (*Repo)(nil)

const _categoryCols = `id::text, slug, name, group_code, poster_url, tags, position`

func scanCategory(row scanner) (entity.Category, error) {
	var (
		c     entity.Category
		group string
	)

	err := row.Scan(&c.ID, &c.Slug, &c.Name, &group, &c.PosterURL, &c.Tags, &c.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Category{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Category{}, fmt.Errorf("scan category: %w", err)
	}

	c.Group = entity.CategoryGroup(group)
	c.Tags = tagsOf(c.Tags)

	return c, nil
}

func (r *Repo) categories(ctx context.Context, q string, args ...any) ([]entity.Category, error) {
	rows, err := r.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - categories: %w", err)
	}
	defer rows.Close()

	out := []entity.Category{}

	for rows.Next() {
		c, serr := scanCategory(rows)
		if serr != nil {
			return nil, fmt.Errorf("Repo - categories: %w", serr)
		}

		out = append(out, c)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - categories: %w", err)
	}

	return out, nil
}

// Categories -.
func (r *Repo) Categories(ctx context.Context, group *entity.CategoryGroup) ([]entity.Category, error) {
	if group != nil {
		return r.categories(ctx,
			`SELECT `+_categoryCols+` FROM categories WHERE group_code = $1 ORDER BY position, slug`, string(*group))
	}

	return r.categories(ctx, `SELECT `+_categoryCols+` FROM categories ORDER BY position, slug`)
}

// BySlug -.
func (r *Repo) BySlug(ctx context.Context, slug string) (entity.Category, error) {
	c, err := scanCategory(r.Pool.QueryRow(ctx, `SELECT `+_categoryCols+` FROM categories WHERE slug = $1`, slug))
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return entity.Category{}, fmt.Errorf("Repo - BySlug: %w", err)
	}

	return c, err
}

// ByIDs -.
func (r *Repo) ByIDs(ctx context.Context, ids []string) (map[string]entity.Category, error) {
	out := make(map[string]entity.Category, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	list, err := r.categories(ctx, `SELECT `+_categoryCols+` FROM categories WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}

	for _, c := range list {
		out[c.ID] = c
	}

	return out, nil
}

// LiveStreamIDsByCategory -.
func (r *Repo) LiveStreamIDsByCategory(ctx context.Context) (map[string][]string, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT category_id::text, stream_id::text FROM live_streams WHERE category_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("Repo - LiveStreamIDsByCategory: %w", err)
	}
	defer rows.Close()

	out := map[string][]string{}

	for rows.Next() {
		var cat, stream string
		if err = rows.Scan(&cat, &stream); err != nil {
			return nil, fmt.Errorf("Repo - LiveStreamIDsByCategory - scan: %w", err)
		}

		out[cat] = append(out[cat], stream)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - LiveStreamIDsByCategory: %w", err)
	}

	return out, nil
}
