package persistent

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
)

// SetLive -.
func (r *Repo) SetLive(ctx context.Context, eventID string, s entity.LiveStream) error {
	return r.inEventTx(ctx, "Repo - SetLive", eventID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE channels SET is_live = true, current_stream_id = $2, updated_at = now() WHERE id = $1`,
			s.ChannelID, s.StreamID)
		if err != nil {
			return fmt.Errorf("Repo - SetLive: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return nil // unknown channel: nothing to mirror
		}

		if _, err = tx.Exec(ctx,
			`INSERT INTO live_streams (stream_id, channel_id, title, category_id, tags, language, is_mature, started_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (channel_id) DO UPDATE
			 SET stream_id = EXCLUDED.stream_id, title = EXCLUDED.title, category_id = EXCLUDED.category_id,
			     tags = EXCLUDED.tags, language = EXCLUDED.language, is_mature = EXCLUDED.is_mature,
			     started_at = EXCLUDED.started_at`,
			s.StreamID, s.ChannelID, s.Title, s.CategoryID, tagsOf(s.Tags), s.Language, s.IsMature, s.StartedAt); err != nil {
			return fmt.Errorf("Repo - SetLive - live stream: %w", err)
		}

		// A missed stream.ended leaves the previous stream's document behind.
		if _, err = tx.Exec(ctx,
			`DELETE FROM search_documents WHERE entity_type = 'STREAM' AND channel_id = $1 AND entity_id <> $2`,
			s.ChannelID, s.StreamID); err != nil {
			return fmt.Errorf("Repo - SetLive - old documents: %w", err)
		}

		if err := upsertChannelDoc(ctx, tx, s.ChannelID); err != nil {
			return err
		}

		return upsertStreamDoc(ctx, tx, s.StreamID)
	})
}

// UpdateLive -.
func (r *Repo) UpdateLive(ctx context.Context, eventID string, s entity.LiveStream) error {
	return r.inEventTx(ctx, "Repo - UpdateLive", eventID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE live_streams SET title = $2, category_id = $3, tags = $4, language = $5, is_mature = $6
			 WHERE stream_id = $1`,
			s.StreamID, s.Title, s.CategoryID, tagsOf(s.Tags), s.Language, s.IsMature)
		if err != nil {
			return fmt.Errorf("Repo - UpdateLive: %w", err)
		}

		if tag.RowsAffected() == 0 {
			return nil // already ended
		}

		return upsertStreamDoc(ctx, tx, s.StreamID)
	})
}

// upsertStreamDoc writes the search document of a stream on air from live_streams and its channel.
func upsertStreamDoc(ctx context.Context, tx pgx.Tx, streamID string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO search_documents AS d (entity_type, entity_id, title, keywords, channel_id, category_id, subtitle,
		                                    image_url, target, is_live, is_mature, updated_at)
		 SELECT 'STREAM', l.stream_id, l.title,
		        concat_ws(' ', array_to_string(l.tags, ' '), c.display_name, c.handle::text, cat.name),
		        c.id, l.category_id, c.display_name, c.avatar_url, '/live/' || l.stream_id::text, true, l.is_mature, now()
		 FROM live_streams l JOIN channels c ON c.id = l.channel_id
		 LEFT JOIN categories cat ON cat.id = l.category_id
		 WHERE l.stream_id = $1 AND c.status = 'ACTIVE'
		 ON CONFLICT (entity_type, entity_id) DO UPDATE
		 SET title = EXCLUDED.title, keywords = EXCLUDED.keywords, category_id = EXCLUDED.category_id,
		     subtitle = EXCLUDED.subtitle, image_url = EXCLUDED.image_url, is_mature = EXCLUDED.is_mature, updated_at = now()`,
		streamID)
	if err != nil {
		return fmt.Errorf("Repo - stream document: %w", err)
	}

	return nil
}

// SetOffline -.
func (r *Repo) SetOffline(ctx context.Context, eventID, channelID, streamID string) error {
	return r.inEventTx(ctx, "Repo - SetOffline", eventID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE channels SET is_live = false, current_stream_id = NULL, last_live_at = now(), updated_at = now()
			 WHERE id = $1 AND (current_stream_id IS NULL OR current_stream_id = $2)`,
			channelID, streamID); err != nil {
			return fmt.Errorf("Repo - SetOffline: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM live_streams WHERE stream_id = $1`, streamID); err != nil {
			return fmt.Errorf("Repo - SetOffline - live stream: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM search_documents WHERE entity_type = 'STREAM' AND entity_id = $1`, streamID); err != nil {
			return fmt.Errorf("Repo - SetOffline - stream document: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE search_documents d SET is_live = c.is_live, viewers = CASE WHEN c.is_live THEN d.viewers ELSE 0 END,
			        updated_at = now()
			 FROM channels c WHERE d.entity_type = 'CHANNEL' AND d.entity_id = c.id AND c.id = $1`, channelID); err != nil {
			return fmt.Errorf("Repo - SetOffline - channel document: %w", err)
		}

		return nil
	})
}

// LiveStreams -.
func (r *Repo) LiveStreams(ctx context.Context, channelIDs []string) (map[string]entity.LiveStream, error) {
	out := make(map[string]entity.LiveStream, len(channelIDs))
	if len(channelIDs) == 0 {
		return out, nil
	}

	rows, err := r.Pool.Query(ctx,
		`SELECT `+_liveCols+` FROM live_streams l WHERE l.channel_id = ANY($1::uuid[])`, channelIDs)
	if err != nil {
		return nil, fmt.Errorf("Repo - LiveStreams: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		s, serr := scanLive(rows)
		if serr != nil {
			return nil, fmt.Errorf("Repo - LiveStreams: %w", serr)
		}

		out[s.ChannelID] = s
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("Repo - LiveStreams: %w", err)
	}

	return out, nil
}

// LiveStreamIDs -.
func (r *Repo) LiveStreamIDs(ctx context.Context) ([]string, error) {
	rows, err := r.Pool.Query(ctx, `SELECT stream_id::text FROM live_streams`)
	if err != nil {
		return nil, fmt.Errorf("Repo - LiveStreamIDs: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("Repo - LiveStreamIDs: %w", err)
	}

	return ids, nil
}

const _liveCols = `l.stream_id::text, l.channel_id::text, l.title, l.category_id::text, l.tags, l.language, l.is_mature,
	l.started_at`

func scanLive(row scanner, extra ...any) (entity.LiveStream, error) {
	var s entity.LiveStream

	dest := append([]any{&s.StreamID, &s.ChannelID, &s.Title, &s.CategoryID, &s.Tags, &s.Language, &s.IsMature, &s.StartedAt},
		extra...)
	if err := row.Scan(dest...); err != nil {
		return entity.LiveStream{}, fmt.Errorf("scan live stream: %w", err)
	}

	s.Tags = tagsOf(s.Tags)

	return s, nil
}

func tagsOf(tags []string) []string {
	if tags == nil {
		return []string{}
	}

	return tags
}
