package persistent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

var _ repo.SearchRepo = (*Repo)(nil)

// _hits matches $2 (the query) against the documents of the types $3. Both sides go through
// lower(immutable_unaccent()), so "lien minh" finds "Liên Minh". The tsquery is built in SQL from
// [:alnum:]_ tokens only, so no tsquery syntax from the input can reach to_tsquery; $1 is the same query with
// LIKE wildcards escaped (prefix match on the title).
const _hits = `WITH q AS (
	SELECT CASE WHEN cardinality(t.toks) = 0 THEN NULL
	            ELSE to_tsquery('simple', array_to_string(ARRAY(SELECT x || ':*' FROM unnest(t.toks) x), ' & ')) END AS tq,
	       lower(immutable_unaccent($1::text)) || '%' AS prefix
	FROM (SELECT array_remove(regexp_split_to_array(lower(immutable_unaccent($2::text)), '[^[:alnum:]_]+'), '') AS toks) t
), hits AS (
	SELECT d.entity_type, d.entity_id, d.title, d.subtitle, d.image_url, d.viewers, d.is_live, d.is_mature, d.target,
	       (d.title_unaccent LIKE q.prefix) AS prefix_hit,
	       COALESCE(round(ts_rank(d.tsv, q.tq) * 1000000)::int, 0) AS rank
	FROM search_documents d, q
	WHERE (d.tsv @@ q.tq OR d.title_unaccent LIKE q.prefix)
	  AND (d.expires_at IS NULL OR d.expires_at > now())
	  AND d.entity_type = ANY($3::text[])
)`

const _hitCols = `lower(entity_type), entity_id::text, title, subtitle, image_url, viewers, is_live, is_mature, target, prefix_hit, rank`

const _hitOrder = `prefix_hit DESC, is_live DESC, rank DESC, viewers DESC, entity_id DESC`

// escapeLike escapes the LIKE wildcards (default escape character is the backslash).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func dbTypes(types []entity.DocType) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, strings.ToUpper(string(t)))
	}

	return out
}

// Suggest -.
func (r *Repo) Suggest(ctx context.Context, q string, types []entity.DocType, limit int) ([]entity.SearchDoc, error) {
	return r.hits(ctx, "Suggest", _hits+`
		SELECT `+_hitCols+` FROM (
			SELECT hits.*, row_number() OVER (PARTITION BY entity_type ORDER BY `+_hitOrder+`) AS rn FROM hits
		) x WHERE rn <= $4 ORDER BY entity_type, rn`,
		escapeLike(q), q, dbTypes(types), limit)
}

// Search -.
func (r *Repo) Search(ctx context.Context, q string, t entity.DocType, after *repo.SearchCursor, limit int) ([]entity.SearchDoc, error) {
	sql := _hits + ` SELECT ` + _hitCols + ` FROM hits`
	args := []any{escapeLike(q), q, dbTypes([]entity.DocType{t}), limit}

	if after != nil {
		sql += ` WHERE (prefix_hit, is_live, rank, viewers, entity_id) < ($5, $6, $7, $8, $9::uuid)`

		args = append(args, after.PrefixHit, after.IsLive, after.Rank, after.Viewers, after.ID)
	}

	return r.hits(ctx, "Search", sql+` ORDER BY `+_hitOrder+` LIMIT $4`, args...)
}

func (r *Repo) hits(ctx context.Context, op, sql string, args ...any) ([]entity.SearchDoc, error) {
	rows, err := r.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}
	defer rows.Close()

	out := []entity.SearchDoc{}

	for rows.Next() {
		var (
			d   entity.SearchDoc
			typ string
		)

		if err = rows.Scan(&typ, &d.ID, &d.Title, &d.Subtitle, &d.ImageURL, &d.Viewers, &d.IsLive, &d.IsMature, &d.Target,
			&d.PrefixHit, &d.Rank); err != nil {
			return nil, fmt.Errorf("Repo - %s - scan: %w", op, err)
		}

		d.Type = entity.DocType(typ)
		out = append(out, d)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - %s: %w", op, err)
	}

	return out, nil
}

// UpsertVOD -.
func (r *Repo) UpsertVOD(ctx context.Context, eventID string, v entity.VODDoc) error {
	return r.inEventTx(ctx, "Repo - UpsertVOD", eventID, func(tx pgx.Tx) error {
		if !v.Public {
			return deleteVODDoc(ctx, tx, v.VODID)
		}

		_, err := tx.Exec(ctx,
			`INSERT INTO search_documents AS d (entity_type, entity_id, title, keywords, channel_id, category_id, subtitle,
			                                    image_url, target, is_mature, expires_at, updated_at)
			 SELECT 'VOD', $1::uuid, $2, concat_ws(' ', c.display_name, c.handle::text, cat.name), c.id, $4, c.display_name,
			        NULLIF($5, ''), '/vod/' || $1::uuid::text, $6, $7, now()
			 FROM channels c LEFT JOIN categories cat ON cat.id = $4::uuid
			 WHERE c.id = $3 AND c.status = 'ACTIVE'
			 ON CONFLICT (entity_type, entity_id) DO UPDATE
			 SET title = EXCLUDED.title, keywords = EXCLUDED.keywords, category_id = EXCLUDED.category_id,
			     image_url = EXCLUDED.image_url, is_mature = EXCLUDED.is_mature, expires_at = EXCLUDED.expires_at,
			     updated_at = now()`,
			v.VODID, v.Title, v.ChannelID, v.CategoryID, v.ThumbnailURL, v.IsMature, v.ExpiresAt)
		if err != nil {
			return fmt.Errorf("Repo - UpsertVOD: %w", err)
		}

		return nil
	})
}

// DeleteVOD -.
func (r *Repo) DeleteVOD(ctx context.Context, eventID, vodID string) error {
	return r.inEventTx(ctx, "Repo - DeleteVOD", eventID, func(tx pgx.Tx) error {
		return deleteVODDoc(ctx, tx, vodID)
	})
}

func deleteVODDoc(ctx context.Context, tx pgx.Tx, vodID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM search_documents WHERE entity_type = 'VOD' AND entity_id = $1`, vodID); err != nil {
		return fmt.Errorf("Repo - delete VOD document: %w", err)
	}

	return nil
}

// SetViewers -.
func (r *Repo) SetViewers(ctx context.Context, counts map[string]int) error {
	ids := make([]string, 0, len(counts))
	ns := make([]int, 0, len(counts))

	for id, n := range counts {
		ids = append(ids, id)
		ns = append(ns, n)
	}

	return r.inTx(ctx, "Repo - SetViewers", func(tx pgx.Tx) error {
		for _, q := range []string{_streamViewers, _channelViewers, _categoryViewers} {
			if _, err := tx.Exec(ctx, q, ids, ns); err != nil {
				return fmt.Errorf("Repo - SetViewers: %w", err)
			}
		}

		return nil
	})
}

const (
	_viewerCounts = `WITH v AS (SELECT * FROM unnest($1::uuid[], $2::int[]) AS v (stream_id, n)) `

	_streamViewers = _viewerCounts + `
		UPDATE search_documents d SET viewers = COALESCE((SELECT n FROM v WHERE v.stream_id = d.entity_id), 0)
		WHERE d.entity_type = 'STREAM'
		  AND d.viewers IS DISTINCT FROM COALESCE((SELECT n FROM v WHERE v.stream_id = d.entity_id), 0)`

	_channelViewers = _viewerCounts + `, c AS (SELECT l.channel_id, v.n FROM live_streams l JOIN v USING (stream_id))
		UPDATE search_documents d SET viewers = COALESCE((SELECT n FROM c WHERE c.channel_id = d.entity_id), 0)
		WHERE d.entity_type = 'CHANNEL'
		  AND d.viewers IS DISTINCT FROM COALESCE((SELECT n FROM c WHERE c.channel_id = d.entity_id), 0)`

	_categoryViewers = _viewerCounts + `, c AS (
			SELECT l.category_id, sum(v.n)::int AS n FROM live_streams l JOIN v USING (stream_id) GROUP BY l.category_id)
		UPDATE search_documents d SET viewers = COALESCE((SELECT n FROM c WHERE c.category_id = d.entity_id), 0)
		WHERE d.entity_type = 'CATEGORY'
		  AND d.viewers IS DISTINCT FROM COALESCE((SELECT n FROM c WHERE c.category_id = d.entity_id), 0)`
)

// History -.
func (r *Repo) History(ctx context.Context, userID string, limit int) ([]entity.SearchQuery, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT id::text, query, created_at FROM search_history WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, fmt.Errorf("Repo - History: %w", err)
	}
	defer rows.Close()

	out := []entity.SearchQuery{}

	for rows.Next() {
		var q entity.SearchQuery
		if err = rows.Scan(&q.ID, &q.Query, &q.CreatedAt); err != nil {
			return nil, fmt.Errorf("Repo - History - scan: %w", err)
		}

		out = append(out, q)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - History: %w", err)
	}

	return out, nil
}

// Remember -.
func (r *Repo) Remember(ctx context.Context, userID, query string, keep int) (entity.SearchQuery, error) {
	var q entity.SearchQuery

	err := r.inTx(ctx, "Repo - Remember", func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO search_history (user_id, query) VALUES ($1, $2)
			 ON CONFLICT (user_id, lower(query)) DO UPDATE SET query = EXCLUDED.query, created_at = now()
			 RETURNING id::text, query, created_at`, userID, query).Scan(&q.ID, &q.Query, &q.CreatedAt); err != nil {
			return fmt.Errorf("Repo - Remember - upsert: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`DELETE FROM search_history WHERE user_id = $1 AND id NOT IN (
				SELECT id FROM search_history WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2)`,
			userID, keep); err != nil {
			return fmt.Errorf("Repo - Remember - trim: %w", err)
		}

		return nil
	})

	return q, err
}

// Forget -.
func (r *Repo) Forget(ctx context.Context, userID string, id *string) error {
	var err error

	if id == nil {
		_, err = r.Pool.Exec(ctx, `DELETE FROM search_history WHERE user_id = $1`, userID)
	} else {
		_, err = r.Pool.Exec(ctx, `DELETE FROM search_history WHERE user_id = $1 AND id = $2`, userID, *id)
	}

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("Repo - Forget: %w", err)
	}

	return nil
}
