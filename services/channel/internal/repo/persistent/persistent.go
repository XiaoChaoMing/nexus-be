// Package persistent implements channel-svc storage on PostgreSQL (database nxl_channel).
package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/minhnn/nexuslive-be/services/channel/internal/repo"
)

const (
	_consumer = "channel-svc"
	// Upper bound of suffixed handle candidates tried for one channel (minhnn2 … minhnn1000).
	_maxHandleCandidates = 1000
	_uniqueViolation     = "23505"
	_handleUnique        = "channels_handle_key"
)

const _channelCols = `c.id::text, c.owner_id::text, c.handle::text, c.display_name, c.bio, c.avatar_url, c.banner_url,
	c.social_links, c.verified, c.mature_default, c.status, c.is_live, c.current_stream_id::text, c.follower_count,
	c.last_live_at, c.handle_changed_at, c.created_at`

// Repo implements the PostgreSQL contracts of repo.
type Repo struct {
	*postgres.Postgres
}

// New -.
func New(pg *postgres.Postgres) *Repo {
	return &Repo{pg}
}

var _ repo.ChannelRepo = (*Repo)(nil)

type scanner interface {
	Scan(dest ...any) error
}

func scanChannel(row scanner, extra ...any) (entity.Channel, error) {
	var (
		c      entity.Channel
		status string
		links  []byte
	)

	dest := append([]any{
		&c.ID, &c.OwnerID, &c.Handle, &c.DisplayName, &c.Bio, &c.AvatarURL, &c.BannerURL, &links,
		&c.Verified, &c.MatureDefault, &status, &c.IsLive, &c.CurrentStreamID, &c.FollowerCount, &c.LastLiveAt,
		&c.HandleChangedAt, &c.CreatedAt,
	}, extra...)

	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Channel{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("scan channel: %w", err)
	}

	c.Status = entity.Status(status)
	c.SocialLinks = []entity.SocialLink{}

	// Rows written before social links were an array hold "{}": no links.
	if len(links) > 0 && links[0] == '[' {
		if err = json.Unmarshal(links, &c.SocialLinks); err != nil {
			return entity.Channel{}, fmt.Errorf("scan channel - social links: %w", err)
		}
	}

	return c, nil
}

func (r *Repo) inTx(ctx context.Context, op string, fn func(tx pgx.Tx) error) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s - begin: %w", op, err)
	}

	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s - commit: %w", op, err)
	}

	return nil
}

// inEventTx runs fn once per event id: a redelivered event finds its processed_events row and is skipped.
func (r *Repo) inEventTx(ctx context.Context, op, eventID string, fn func(tx pgx.Tx) error) error {
	return r.inTx(ctx, op, func(tx pgx.Tx) error {
		if eventID != "" {
			tag, err := tx.Exec(ctx,
				`INSERT INTO processed_events (event_id, consumer) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				eventID, _consumer)
			if err != nil {
				return fmt.Errorf("%s - processed_events: %w", op, err)
			}

			if tag.RowsAffected() == 0 {
				return nil // duplicate delivery
			}
		}

		return fn(tx)
	})
}

func addOutbox(ctx context.Context, tx pgx.Tx, aggregate, aggregateID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox marshal: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload) VALUES ($1, $2, $3, $4)`,
		aggregate, aggregateID, eventType, b)
	if err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}

	return nil
}

func (r *Repo) channelWhere(ctx context.Context, op, where string, arg any) (entity.Channel, error) {
	c, err := scanChannel(r.Pool.QueryRow(ctx, `SELECT `+_channelCols+` FROM channels c WHERE `+where, arg))
	if err != nil && !errors.Is(err, entity.ErrNotFound) {
		return entity.Channel{}, fmt.Errorf("Repo - %s: %w", op, err)
	}

	return c, err
}

// ByHandle -.
func (r *Repo) ByHandle(ctx context.Context, handle string) (entity.Channel, error) {
	return r.channelWhere(ctx, "ByHandle", "c.handle = $1", handle)
}

// ByID -.
func (r *Repo) ByID(ctx context.Context, id string) (entity.Channel, error) {
	return r.channelWhere(ctx, "ByID", "c.id = $1", id)
}

// ByOwner -.
func (r *Repo) ByOwner(ctx context.Context, ownerID string) (entity.Channel, error) {
	return r.channelWhere(ctx, "ByOwner", "c.owner_id = $1", ownerID)
}

// CreateForOwner -.
func (r *Repo) CreateForOwner(ctx context.Context, eventID string, in repo.NewChannel) (entity.Channel, bool, error) {
	var (
		ch      entity.Channel
		created bool
	)

	err := r.inEventTx(ctx, "Repo - CreateForOwner", eventID, func(tx pgx.Tx) error {
		var err error

		ch, created, err = createChannel(ctx, tx, in)
		if err != nil || !created {
			return err
		}

		if err := upsertChannelDoc(ctx, tx, ch.ID); err != nil {
			return err
		}

		return addOutbox(ctx, tx, "channel", ch.ID, events.TypeChannelCreated, events.ChannelCreated{
			ChannelID: ch.ID, OwnerID: ch.OwnerID, Handle: ch.Handle, DisplayName: ch.DisplayName,
		})
	})
	if err != nil {
		return entity.Channel{}, false, err
	}

	return ch, created, nil
}

// createChannel tries handle candidates in order. ON CONFLICT DO NOTHING covers both unique keys, so
// an empty insert is either the owner's existing channel (concurrent or earlier approval) or a
// taken handle; the owner lookup tells them apart without aborting the transaction.
func createChannel(ctx context.Context, tx pgx.Tx, in repo.NewChannel) (entity.Channel, bool, error) {
	for n := 1; n <= _maxHandleCandidates; n++ {
		existing, err := scanChannel(tx.QueryRow(ctx, `SELECT `+_channelCols+` FROM channels c WHERE owner_id = $1`, in.OwnerID))
		if err == nil {
			return existing, false, nil
		}

		if !errors.Is(err, entity.ErrNotFound) {
			return entity.Channel{}, false, fmt.Errorf("Repo - CreateForOwner - owner: %w", err)
		}

		ch, err := scanChannel(tx.QueryRow(ctx,
			`INSERT INTO channels AS c (owner_id, handle, display_name) VALUES ($1, $2, $3)
			 ON CONFLICT DO NOTHING
			 RETURNING `+_channelCols,
			in.OwnerID, entity.HandleCandidate(in.BaseHandle, n), in.DisplayName))
		if err == nil {
			return ch, true, nil
		}

		if !errors.Is(err, entity.ErrNotFound) {
			return entity.Channel{}, false, fmt.Errorf("Repo - CreateForOwner - insert: %w", err)
		}
	}

	return entity.Channel{}, false, entity.ErrNoFreeHandle
}

// upsertChannelDoc writes the channel's search document from its row (a suspended channel has none).
func upsertChannelDoc(ctx context.Context, tx pgx.Tx, channelID string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO search_documents AS d (entity_type, entity_id, title, keywords, channel_id, subtitle, image_url, target,
		                                    is_live, updated_at)
		 SELECT 'CHANNEL', id, display_name, handle::text, id, '@' || handle, avatar_url, '/' || handle, is_live, now()
		 FROM channels WHERE id = $1 AND status = 'ACTIVE'
		 ON CONFLICT (entity_type, entity_id) DO UPDATE
		 SET title = EXCLUDED.title, keywords = EXCLUDED.keywords, subtitle = EXCLUDED.subtitle,
		     image_url = EXCLUDED.image_url, target = EXCLUDED.target, is_live = EXCLUDED.is_live, updated_at = now()`,
		channelID)
	if err != nil {
		return fmt.Errorf("Repo - channel document: %w", err)
	}

	return nil
}

// UpdateProfile -.
func (r *Repo) UpdateProfile(ctx context.Context, channelID string, p entity.ProfilePatch, now time.Time) (entity.Channel, error) {
	var ch entity.Channel

	err := r.inTx(ctx, "Repo - UpdateProfile", func(tx pgx.Tx) error {
		old, err := scanChannel(tx.QueryRow(ctx, `SELECT `+_channelCols+` FROM channels c WHERE id = $1 FOR UPDATE`, channelID))
		if err != nil {
			return err
		}

		ch, err = updateChannelRow(ctx, tx, channelID, p, now)
		if err != nil {
			return err
		}

		if old.DisplayName == ch.DisplayName && old.Handle == ch.Handle {
			return nil
		}

		if err := upsertChannelDoc(ctx, tx, channelID); err != nil {
			return err
		}

		if _, err = tx.Exec(ctx,
			`UPDATE search_documents SET subtitle = $2, updated_at = now() WHERE channel_id = $1 AND entity_type IN ('STREAM', 'VOD')`,
			channelID, ch.DisplayName); err != nil {
			return fmt.Errorf("Repo - UpdateProfile - documents: %w", err)
		}

		return addOutbox(ctx, tx, "channel", ch.ID, events.TypeChannelUpdated, channelUpdated(ch))
	})

	return ch, err
}

func updateChannelRow(ctx context.Context, tx pgx.Tx, channelID string, p entity.ProfilePatch, now time.Time) (entity.Channel, error) {
	var links []byte

	if p.SocialLinks != nil {
		b, err := json.Marshal(*p.SocialLinks)
		if err != nil {
			return entity.Channel{}, fmt.Errorf("Repo - UpdateProfile - links: %w", err)
		}

		links = b
	}

	ch, err := scanChannel(tx.QueryRow(ctx,
		`UPDATE channels AS c SET
		    display_name = COALESCE($2, display_name),
		    bio = CASE WHEN $3 THEN NULL ELSE COALESCE($4, bio) END,
		    social_links = COALESCE($5::jsonb, social_links),
		    mature_default = COALESCE($6, mature_default),
		    handle_changed_at = CASE WHEN $7::text IS NOT NULL AND lower($7::text) <> lower(handle::text) THEN $8
		                             ELSE handle_changed_at END,
		    handle = COALESCE($7::text, handle::text),
		    updated_at = now()
		 WHERE id = $1
		 RETURNING `+_channelCols,
		channelID, p.DisplayName, p.ClearBio, p.Bio, links, p.MatureDefault, p.Handle, now))

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == _uniqueViolation && pgErr.ConstraintName == _handleUnique {
		return entity.Channel{}, entity.ErrHandleTaken
	}

	if err != nil {
		return entity.Channel{}, fmt.Errorf("Repo - UpdateProfile - update: %w", err)
	}

	return ch, nil
}

func channelUpdated(ch entity.Channel) events.ChannelUpdated {
	return events.ChannelUpdated{
		ChannelID: ch.ID, OwnerID: ch.OwnerID, Handle: ch.Handle, DisplayName: ch.DisplayName, AvatarURL: ch.AvatarURL,
	}
}

// SetImage -.
func (r *Repo) SetImage(ctx context.Context, channelID string, kind entity.ImageKind, url string) (*string, error) {
	var old *string

	err := r.inTx(ctx, "Repo - SetImage", func(tx pgx.Tx) error {
		col := "avatar_url"
		if kind == entity.ImageBanner {
			col = "banner_url"
		}

		// col is one of two constants, never input.
		if err := tx.QueryRow(ctx, `SELECT `+col+` FROM channels WHERE id = $1 FOR UPDATE`, channelID).Scan(&old); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return entity.ErrNotFound
			}

			return fmt.Errorf("Repo - SetImage - select: %w", err)
		}

		ch, err := scanChannel(tx.QueryRow(ctx,
			`UPDATE channels AS c SET `+col+` = $2, updated_at = now() WHERE id = $1 RETURNING `+_channelCols, channelID, url))
		if err != nil {
			return fmt.Errorf("Repo - SetImage - update: %w", err)
		}

		if kind != entity.ImageAvatar {
			return nil
		}

		if err := upsertChannelDoc(ctx, tx, channelID); err != nil {
			return err
		}

		if _, err = tx.Exec(ctx,
			`UPDATE search_documents SET image_url = $2, updated_at = now() WHERE channel_id = $1 AND entity_type = 'STREAM'`,
			channelID, url); err != nil {
			return fmt.Errorf("Repo - SetImage - documents: %w", err)
		}

		return addOutbox(ctx, tx, "channel", ch.ID, events.TypeChannelUpdated, channelUpdated(ch))
	})

	return old, err
}
