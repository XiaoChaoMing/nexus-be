package persistent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/services/safety/internal/entity"
	"github.com/minhnn/nexuslive-be/services/safety/internal/repo"
)

const (
	_userCols = `u.id::text, u.username::text, u.display_name, u.global_role, u.status, u.locked_until, u.created_at,
	(SELECT count(*) FROM reports r WHERE r.target_user_id = u.id AND r.status IN ` + _open + `)`
	_channelCols = `c.id::text, c.owner_id::text, coalesce(o.username::text, ''), c.handle::text, c.display_name,
	c.avatar_url, c.status, c.suspended_at,
	(SELECT s.id::text FROM streams s WHERE s.channel_id = c.id AND s.status IN ('LIVE', 'INTERRUPTED')
		ORDER BY s.started_at DESC NULLS LAST LIMIT 1),
	(SELECT count(*) FROM reports r WHERE r.status IN ` + _open + ` AND ((r.target_type = 'CHANNEL' AND r.target_id = c.id)
		OR (r.target_type = 'STREAM' AND r.target_id IN (SELECT s.id FROM streams s WHERE s.channel_id = c.id))))`
	_channelFrom = ` FROM channels c LEFT JOIN users o ON o.id = c.owner_id`
	_streamCols  = `id::text, channel_id::text, title, status, end_reason, vod_id::text, vod_deleted, started_at, ended_at`
	// Status filters on the effective state: an expired lock reads as ACTIVE.
	_userActive = `(u.status = 'ACTIVE' OR (u.status = 'LOCKED' AND u.locked_until <= now()))`
	_userLocked = `(u.status = 'LOCKED' AND (u.locked_until IS NULL OR u.locked_until > now()))`
)

func scanUser(row scanner) (entity.UserCopy, error) {
	var u entity.UserCopy

	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.GlobalRole, &u.Status, &u.LockedUntil, &u.CreatedAt,
		&u.OpenReports)

	return u, err //nolint:wrapcheck // callers wrap
}

func scanChannel(row scanner) (entity.ChannelCopy, error) {
	var c entity.ChannelCopy

	err := row.Scan(&c.ID, &c.OwnerID, &c.Owner.Username, &c.Handle, &c.DisplayName, &c.AvatarURL, &c.Status,
		&c.SuspendedAt, &c.CurrentStreamID, &c.OpenReports)
	c.Owner.ID = c.OwnerID

	return c, err //nolint:wrapcheck // callers wrap
}

func scanStream(row scanner) (entity.StreamCopy, error) {
	var s entity.StreamCopy

	err := row.Scan(&s.ID, &s.ChannelID, &s.Title, &s.Status, &s.EndReason, &s.VODID, &s.VODDeleted, &s.StartedAt,
		&s.EndedAt)

	return s, err //nolint:wrapcheck // callers wrap
}

// User -.
func (r *Repo) User(ctx context.Context, id string) (entity.UserCopy, error) {
	u, err := scanUser(r.Pool.QueryRow(ctx, `SELECT `+_userCols+` FROM users u WHERE u.id = $1`, id))
	if err != nil {
		return u, fmt.Errorf("ReplicaRepo - User: %w", notFound(err))
	}

	return u, nil
}

// Channel -.
func (r *Repo) Channel(ctx context.Context, id string) (entity.ChannelCopy, error) {
	c, err := scanChannel(r.Pool.QueryRow(ctx, `SELECT `+_channelCols+_channelFrom+` WHERE c.id = $1`, id))
	if err != nil {
		return c, fmt.Errorf("ReplicaRepo - Channel: %w", notFound(err))
	}

	return c, nil
}

// Stream -.
func (r *Repo) Stream(ctx context.Context, id string) (entity.StreamCopy, error) {
	s, err := scanStream(r.Pool.QueryRow(ctx, `SELECT `+_streamCols+` FROM streams WHERE id = $1`, id))
	if err != nil {
		return s, fmt.Errorf("ReplicaRepo - Stream: %w", notFound(err))
	}

	return s, nil
}

// Lookup loads the copies of a page in three queries.
func (r *Repo) Lookup(ctx context.Context, ids repo.LookupIDs) (repo.Lookup, error) {
	out := repo.Lookup{
		Users:    map[string]entity.UserCopy{},
		Channels: map[string]entity.ChannelCopy{},
		Streams:  map[string]entity.StreamCopy{},
	}

	err := errors.Join(
		lookup(ctx, r, `SELECT `+_userCols+` FROM users u WHERE u.id = ANY($1)`, ids.Users, scanUser,
			func(u entity.UserCopy) { out.Users[u.ID] = u }),
		lookup(ctx, r, `SELECT `+_channelCols+_channelFrom+` WHERE c.id = ANY($1)`, ids.Channels, scanChannel,
			func(c entity.ChannelCopy) { out.Channels[c.ID] = c }),
		lookup(ctx, r, `SELECT `+_streamCols+` FROM streams WHERE id = ANY($1)`, ids.Streams, scanStream,
			func(s entity.StreamCopy) { out.Streams[s.ID] = s }),
	)
	if err != nil {
		return repo.Lookup{}, fmt.Errorf("ReplicaRepo - Lookup: %w", err)
	}

	return out, nil
}

func lookup[T any](ctx context.Context, r *Repo, sql string, ids []string, scan func(scanner) (T, error), put func(T)) error {
	ids = validIDs(ids)
	if len(ids) == 0 {
		return nil
	}

	rows, err := r.Pool.Query(ctx, sql, ids)
	if err != nil {
		return err //nolint:wrapcheck // wrapped by Lookup
	}
	defer rows.Close()

	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return err
		}

		put(v)
	}

	return rows.Err() //nolint:wrapcheck // wrapped by Lookup
}

// validIDs drops duplicates and anything that is not a UUID.
func validIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))

	for _, id := range ids {
		if seen[id] || uuid.Validate(id) != nil {
			continue
		}

		seen[id] = true
		out = append(out, id)
	}

	return out
}

// SearchUsers: a UUID matches the id, anything else a username prefix (case-insensitive), by username.
func (r *Repo) SearchUsers(ctx context.Context, q string, status *string, after *repo.NameCursor, limit int,
) ([]entity.UserCopy, error) {
	w := &where{}

	switch {
	case q == "":
	case uuid.Validate(q) == nil:
		w.add("u.id = ?", q)
	default:
		w.add(`lower(u.username::text) LIKE ? || '%'`, strings.ToLower(likeEscape(q)))
	}

	if status != nil {
		w.add(statusCond(*status))
	}

	if after != nil {
		w.add("(lower(u.username::text), u.id) > (?, ?)", after.Name, after.ID)
	}

	rows, err := r.Pool.Query(ctx, `SELECT `+_userCols+` FROM users u`+w.sql()+
		` ORDER BY lower(u.username::text), u.id LIMIT `+w.arg(limit), w.args...)
	if err != nil {
		return nil, fmt.Errorf("ReplicaRepo - SearchUsers: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.UserCopy, error) { return scanUser(row) })
	if err != nil {
		return nil, fmt.Errorf("ReplicaRepo - SearchUsers - scan: %w", err)
	}

	return out, nil
}

func statusCond(status string) string {
	switch status {
	case entity.UserActive:
		return _userActive
	case entity.UserLocked:
		return _userLocked
	}

	return "u.status = 'DELETED'"
}

// SearchChannels: a UUID matches the id, anything else a handle prefix or a display-name substring, by handle.
func (r *Repo) SearchChannels(ctx context.Context, q string, status *string, after *repo.NameCursor, limit int,
) ([]entity.ChannelCopy, error) {
	w := &where{}

	switch {
	case q == "":
	case uuid.Validate(q) == nil:
		w.add("c.id = ?", q)
	default:
		term := strings.ToLower(likeEscape(q))
		w.add(`(lower(c.handle::text) LIKE ? || '%' OR lower(c.display_name) LIKE '%' || ? || '%')`, term, term)
	}

	if status != nil {
		w.add("c.status = ?", *status)
	}

	if after != nil {
		w.add("(lower(c.handle::text), c.id) > (?, ?)", after.Name, after.ID)
	}

	rows, err := r.Pool.Query(ctx, `SELECT `+_channelCols+_channelFrom+w.sql()+
		` ORDER BY lower(c.handle::text), c.id LIMIT `+w.arg(limit), w.args...)
	if err != nil {
		return nil, fmt.Errorf("ReplicaRepo - SearchChannels: %w", err)
	}

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (entity.ChannelCopy, error) { return scanChannel(row) })
	if err != nil {
		return nil, fmt.Errorf("ReplicaRepo - SearchChannels - scan: %w", err)
	}

	return out, nil
}

// PatchUser applies p unless the copy was synced after at.
func (r *Repo) PatchUser(ctx context.Context, eventID string, at time.Time, p repo.UserPatch) error {
	const set = `display_name = coalesce($3, users.display_name), global_role = coalesce($4, users.global_role),
		status = coalesce($5, users.status),
		locked_until = CASE WHEN $5::text IS NULL THEN users.locked_until ELSE $6 END,
		created_at = coalesce($7, users.created_at), synced_at = greatest(users.synced_at, $8)`

	sql := `UPDATE users SET ` + set + ` WHERE id = $1 AND synced_at <= $8 AND ($2::text IS NULL OR true)`
	if p.Username != nil {
		sql = `INSERT INTO users (id, username, display_name, global_role, status, locked_until, created_at, synced_at)
			VALUES ($1, $2, coalesce($3, ''), coalesce($4, 'USER'), coalesce($5, 'ACTIVE'), $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET username = EXCLUDED.username, ` + set + ` WHERE users.synced_at <= $8`
	}

	err := r.inEventTx(ctx, "ReplicaRepo - PatchUser", eventID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, p.ID, p.Username, p.DisplayName, p.GlobalRole, p.Status, p.LockedUntil, p.CreatedAt, at)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("ReplicaRepo - PatchUser: %w", err)
	}

	return nil
}

// PatchChannel applies p unless the copy was synced after at.
func (r *Repo) PatchChannel(ctx context.Context, eventID string, at time.Time, p repo.ChannelPatch) error {
	const set = `owner_id = coalesce($2, channels.owner_id), display_name = coalesce($4, channels.display_name),
		avatar_url = CASE WHEN $6 THEN $5 ELSE channels.avatar_url END, status = coalesce($7, channels.status),
		suspended_at = CASE WHEN $7::text IS NULL THEN channels.suspended_at ELSE $8 END,
		synced_at = greatest(channels.synced_at, $9)`

	sql := `UPDATE channels SET ` + set + ` WHERE id = $1 AND synced_at <= $9 AND ($3::text IS NULL OR true)`
	if p.Handle != nil && p.OwnerID != nil {
		sql = `INSERT INTO channels (id, owner_id, handle, display_name, avatar_url, status, suspended_at, synced_at)
			VALUES ($1, $2, $3, coalesce($4, ''), $5, coalesce($7, 'ACTIVE'), $8, $9)
			ON CONFLICT (id) DO UPDATE SET handle = EXCLUDED.handle, ` + set + ` WHERE channels.synced_at <= $9`
	}

	err := r.inEventTx(ctx, "ReplicaRepo - PatchChannel", eventID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, p.ID, p.OwnerID, p.Handle, p.DisplayName, p.AvatarURL, p.SetAvatar, p.Status,
			p.SuspendedAt, at)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("ReplicaRepo - PatchChannel: %w", err)
	}

	return nil
}

// PatchStream applies p unless the copy was synced after at; VOD fields are never undone.
func (r *Repo) PatchStream(ctx context.Context, eventID string, at time.Time, p repo.StreamPatch) error {
	err := r.inEventTx(ctx, "ReplicaRepo - PatchStream", eventID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO streams
			(id, channel_id, title, status, end_reason, vod_id, vod_deleted, started_at, ended_at, synced_at)
			VALUES ($1, $2, coalesce($3, ''), coalesce($4, 'ENDED'), $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO UPDATE SET title = coalesce($3, streams.title),
				status = CASE WHEN streams.synced_at <= $10 THEN coalesce($4, streams.status) ELSE streams.status END,
				end_reason = coalesce($5, streams.end_reason), vod_id = coalesce($6, streams.vod_id),
				vod_deleted = streams.vod_deleted OR $7, started_at = coalesce(streams.started_at, $8),
				ended_at = coalesce($9, streams.ended_at), synced_at = greatest(streams.synced_at, $10)`,
			p.ID, p.ChannelID, p.Title, p.Status, p.EndReason, p.VODID, p.VODDeleted, p.StartedAt, p.EndedAt, at)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("ReplicaRepo - PatchStream: %w", err)
	}

	return nil
}

// BackfillDone -.
func (r *Repo) BackfillDone(ctx context.Context, name string) (bool, error) {
	var ok bool

	err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM replica_backfills WHERE name = $1)`, name).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("ReplicaRepo - BackfillDone: %w", err)
	}

	return ok, nil
}

// MarkBackfilled -.
func (r *Repo) MarkBackfilled(ctx context.Context, name string) error {
	if _, err := r.Pool.Exec(ctx, `INSERT INTO replica_backfills (name) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
		return fmt.Errorf("ReplicaRepo - MarkBackfilled: %w", err)
	}

	return nil
}
