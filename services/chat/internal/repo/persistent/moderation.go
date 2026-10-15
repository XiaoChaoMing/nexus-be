package persistent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
)

var (
	_ repo.ModerationRepo = (*Repo)(nil)
	_ repo.TermRepo       = (*Repo)(nil)
)

// _createdSlack bounds the created_at of a message around the time in its uuidv7 id, so a lookup by id reads
// one daily partition instead of all of them.
const _createdSlack = time.Minute

const (
	_uuidTimeBytes = 6
	_uuidV7        = 7
)

// createdRange returns the created_at window of a uuidv7 message id (the whole range for other ids).
func createdRange(id string) (from, to time.Time) {
	u, err := uuid.Parse(id)
	if err != nil || u.Version() != _uuidV7 {
		return time.Unix(0, 0), time.Now().Add(24 * time.Hour) //nolint:mnd // any time
	}

	var b [8]byte

	copy(b[2:], u[:_uuidTimeBytes])
	at := time.UnixMilli(int64(binary.BigEndian.Uint64(b[:]))) //nolint:gosec // 48-bit milliseconds

	return at.Add(-_createdSlack), at.Add(_createdSlack)
}

// Message -.
func (r *Repo) Message(ctx context.Context, streamID, messageID string) (entity.Message, error) {
	from, to := createdRange(messageID)

	rows, err := r.Pool.Query(ctx, `SELECT `+_messageCols+`
		FROM chat_messages m LEFT JOIN users u ON u.id = m.user_id
		WHERE m.id = $1 AND m.stream_id = $2 AND m.created_at BETWEEN $3 AND $4`, messageID, streamID, from, to)
	if err != nil {
		return entity.Message{}, fmt.Errorf("Repo - Message: %w", err)
	}

	m, err := pgx.CollectExactlyOneRow(rows, scanMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Message{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.Message{}, fmt.Errorf("Repo - Message: %w", err)
	}

	return m, nil
}

// insertAction stores a mod_actions row and queues chat.mod_action.v1 with the original text of the message
// it concerns (body; audit only).
func insertAction(ctx context.Context, tx pgx.Tx, a entity.ModAction, body *string) error {
	err := tx.QueryRow(ctx,
		`INSERT INTO mod_actions (channel_id, stream_id, actor_id, actor_role, type, target_user_id, message_id, reason, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id::text, created_at`,
		a.ChannelID, a.StreamID, a.ActorID, string(a.ActorRole), a.Type, a.TargetUserID, a.MessageID, a.Reason, a.ExpiresAt,
	).Scan(&a.ID, &a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert mod action: %w", err)
	}

	return addOutboxAs(ctx, tx, _aggregateChannel, a.ChannelID, events.TypeChatModAction, events.ChatModAction{
		ActionID: a.ID, ChannelID: a.ChannelID, StreamID: a.StreamID, ActorID: a.ActorID, ActorRole: string(a.ActorRole),
		Type: a.Type, TargetUserID: a.TargetUserID, MessageID: a.MessageID, MessageBody: body, Reason: a.Reason,
		ExpiresAt: a.ExpiresAt, OccurredAt: a.CreatedAt.UTC(),
	})
}

// DeleteMessage -.
func (r *Repo) DeleteMessage(ctx context.Context, a entity.ModAction) (deleted, wasPinned bool, err error) {
	if a.MessageID == nil || a.StreamID == nil {
		return false, false, entity.ErrNotFound
	}

	from, to := createdRange(*a.MessageID)

	err = r.inTx(ctx, "Repo - DeleteMessage", func(tx pgx.Tx) error {
		var (
			body   string
			gone   bool
			pinned bool
		)

		qerr := tx.QueryRow(ctx,
			`SELECT body, deleted_at IS NOT NULL, pinned FROM chat_messages
			 WHERE id = $1 AND stream_id = $2 AND created_at BETWEEN $3 AND $4 FOR UPDATE`,
			*a.MessageID, *a.StreamID, from, to).Scan(&body, &gone, &pinned)
		if errors.Is(qerr, pgx.ErrNoRows) {
			return entity.ErrNotFound
		}

		if qerr != nil {
			return fmt.Errorf("Repo - DeleteMessage - read: %w", qerr)
		}

		if gone {
			return nil
		}

		if _, qerr = tx.Exec(ctx,
			`UPDATE chat_messages SET deleted_at = now(), deleted_by = $3, pinned = false
			 WHERE id = $1 AND created_at BETWEEN $4 AND $5 AND stream_id = $2`,
			*a.MessageID, *a.StreamID, a.ActorID, from, to); qerr != nil {
			return fmt.Errorf("Repo - DeleteMessage - update: %w", qerr)
		}

		deleted, wasPinned = true, pinned

		return insertAction(ctx, tx, a, &body)
	})

	return deleted, wasPinned, err
}

// lockRoom locks the room row (pins are serialized per room) and returns its status.
func lockRoom(ctx context.Context, tx pgx.Tx, streamID string) (entity.RoomStatus, error) {
	var status string

	err := tx.QueryRow(ctx, `SELECT status FROM chat_rooms WHERE stream_id = $1 FOR UPDATE`, streamID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entity.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("lock room: %w", err)
	}

	return entity.RoomStatus(status), nil
}

// Pin -.
func (r *Repo) Pin(ctx context.Context, a entity.ModAction) (entity.Message, error) {
	if a.MessageID == nil || a.StreamID == nil {
		return entity.Message{}, entity.ErrNotFound
	}

	var m entity.Message

	err := r.inTx(ctx, "Repo - Pin", func(tx pgx.Tx) error {
		status, err := lockRoom(ctx, tx, *a.StreamID)
		if err != nil {
			return err
		}

		if status != entity.RoomOpen {
			return entity.ErrRoomReadOnly
		}

		if m, err = pinnable(ctx, tx, *a.StreamID, *a.MessageID); err != nil {
			return err
		}

		return pinTx(ctx, tx, a, &m)
	})
	if err != nil {
		return entity.Message{}, err
	}

	return m, nil
}

// pinnable reads the message to pin: entity.ErrInvalidState for a SYSTEM or deleted one.
func pinnable(ctx context.Context, tx pgx.Tx, streamID, messageID string) (entity.Message, error) {
	from, to := createdRange(messageID)

	rows, err := tx.Query(ctx, `SELECT `+_messageCols+`
		FROM chat_messages m LEFT JOIN users u ON u.id = m.user_id
		WHERE m.id = $1 AND m.stream_id = $2 AND m.created_at BETWEEN $3 AND $4`, messageID, streamID, from, to)
	if err != nil {
		return entity.Message{}, fmt.Errorf("Repo - Pin - read: %w", err)
	}

	m, err := pgx.CollectExactlyOneRow(rows, scanMessage)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return entity.Message{}, entity.ErrNotFound
	case err != nil:
		return entity.Message{}, fmt.Errorf("Repo - Pin - read: %w", err)
	case m.Deleted || m.Type != entity.MessageNormal:
		return entity.Message{}, entity.ErrInvalidState
	}

	return m, nil
}

// pinTx moves the room's pin to m; pinning the pinned message again changes nothing.
func pinTx(ctx context.Context, tx pgx.Tx, a entity.ModAction, m *entity.Message) error {
	if m.Pinned {
		return nil
	}

	if _, err := tx.Exec(ctx, `UPDATE chat_messages SET pinned = false WHERE stream_id = $1 AND pinned`, m.StreamID); err != nil {
		return fmt.Errorf("Repo - Pin - unpin previous: %w", err)
	}

	from, to := createdRange(m.ID)
	if _, err := tx.Exec(ctx,
		`UPDATE chat_messages SET pinned = true WHERE id = $1 AND stream_id = $2 AND created_at BETWEEN $3 AND $4`,
		m.ID, m.StreamID, from, to); err != nil {
		return fmt.Errorf("Repo - Pin - pin: %w", err)
	}

	m.Pinned = true

	return insertAction(ctx, tx, a, &m.Body)
}

// Unpin -.
func (r *Repo) Unpin(ctx context.Context, a entity.ModAction) (bool, error) {
	if a.StreamID == nil {
		return false, entity.ErrNotFound
	}

	unpinned := false

	err := r.inTx(ctx, "Repo - Unpin", func(tx pgx.Tx) error {
		if _, err := lockRoom(ctx, tx, *a.StreamID); err != nil {
			return err
		}

		var id string

		err := tx.QueryRow(ctx,
			`UPDATE chat_messages SET pinned = false WHERE stream_id = $1 AND pinned RETURNING id::text`, *a.StreamID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("Repo - Unpin: %w", err)
		}

		unpinned = true
		a.MessageID = &id

		return insertAction(ctx, tx, a, nil)
	})

	return unpinned, err
}

// lockChannel serializes the restrictions (and roles, terms) of one channel: two moderators acting on the same
// user cannot interleave their read and write.
func lockChannel(ctx context.Context, tx pgx.Tx, channelID string) error {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM channels WHERE channel_id = $1 FOR UPDATE`, channelID); err != nil {
		return fmt.Errorf("lock channel: %w", err)
	}

	return nil
}

type lastRestriction struct {
	typ     string
	expires *time.Time
}

// liftedBy reports whether an UNBAN / UNTIMEOUT would lift this restriction (it is in force).
func (l lastRestriction) liftedBy(typ string) bool {
	switch typ {
	case entity.ModUnban:
		return l.typ == entity.ModBan
	case entity.ModUntimeout:
		return l.typ == entity.ModTimeout && l.expires != nil && l.expires.After(time.Now())
	}

	return false
}

func latestRestriction(ctx context.Context, tx pgx.Tx, channelID, userID string) (lastRestriction, error) {
	var l lastRestriction

	err := tx.QueryRow(ctx,
		`SELECT type, expires_at FROM mod_actions
		 WHERE channel_id = $1 AND target_user_id = $2 AND type IN ('BAN', 'UNBAN', 'TIMEOUT', 'UNTIMEOUT')
		 ORDER BY created_at DESC, id DESC LIMIT 1`, channelID, userID).Scan(&l.typ, &l.expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return lastRestriction{}, nil
	}

	if err != nil {
		return lastRestriction{}, fmt.Errorf("latest restriction: %w", err)
	}

	return l, nil
}

func openRoomsTx(ctx context.Context, tx pgx.Tx, channelID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT stream_id::text FROM chat_rooms WHERE channel_id = $1 AND status = 'OPEN'`, channelID)
	if err != nil {
		return nil, fmt.Errorf("open rooms: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("open rooms: %w", err)
	}

	return ids, nil
}

// Restrict -.
func (r *Repo) Restrict(ctx context.Context, a entity.ModAction) (openRooms, unpinned []string, err error) {
	if a.TargetUserID == nil {
		return nil, nil, entity.ErrNotFound
	}

	err = r.inTx(ctx, "Repo - Restrict", func(tx pgx.Tx) error {
		if lerr := lockChannel(ctx, tx, a.ChannelID); lerr != nil {
			return lerr
		}

		last, lerr := latestRestriction(ctx, tx, a.ChannelID, *a.TargetUserID)
		if lerr != nil {
			return lerr
		}

		if a.Type == entity.ModTimeout && last.typ == entity.ModBan {
			return entity.ErrInvalidState
		}

		if openRooms, lerr = openRoomsTx(ctx, tx, a.ChannelID); lerr != nil {
			return lerr
		}

		if len(openRooms) > 0 {
			a.StreamID = &openRooms[0]
		}

		if unpinned, lerr = clearMessages(ctx, tx, a.ChannelID, *a.TargetUserID, a.ActorID); lerr != nil {
			return lerr
		}

		return insertAction(ctx, tx, a, nil)
	})

	return openRooms, unpinned, err
}

// clearMessages soft-deletes the user's messages of the channel's OPEN rooms and returns the rooms whose
// pinned message was among them.
func clearMessages(ctx context.Context, tx pgx.Tx, channelID, userID, actorID string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`WITH target AS (
			SELECT m.id, m.created_at, m.stream_id, m.pinned
			FROM chat_messages m JOIN chat_rooms r ON r.stream_id = m.stream_id
			WHERE r.channel_id = $1 AND r.status = 'OPEN' AND m.user_id = $2 AND m.deleted_at IS NULL
			  AND m.created_at >= COALESCE(r.opened_at, '-infinity')
			FOR UPDATE OF m
		 ), cleared AS (
			UPDATE chat_messages c SET deleted_at = now(), deleted_by = $3, pinned = false
			FROM target t WHERE c.id = t.id AND c.created_at = t.created_at
			RETURNING c.id
		 )
		 SELECT DISTINCT t.stream_id::text FROM target t WHERE t.pinned AND EXISTS (SELECT 1 FROM cleared)`,
		channelID, userID, actorID)
	if err != nil {
		return nil, fmt.Errorf("clear messages: %w", err)
	}

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("clear messages: %w", err)
	}

	return ids, nil
}

// Lift -.
func (r *Repo) Lift(ctx context.Context, a entity.ModAction) (lifted bool, rooms []string, err error) {
	if a.TargetUserID == nil {
		return false, nil, entity.ErrNotFound
	}

	err = r.inTx(ctx, "Repo - Lift", func(tx pgx.Tx) error {
		if err := lockChannel(ctx, tx, a.ChannelID); err != nil {
			return err
		}

		last, err := latestRestriction(ctx, tx, a.ChannelID, *a.TargetUserID)
		if err != nil {
			return err
		}

		if !last.liftedBy(a.Type) {
			return nil
		}

		if rooms, err = openRoomsTx(ctx, tx, a.ChannelID); err != nil {
			return err
		}

		if len(rooms) > 0 {
			a.StreamID = &rooms[0]
		}

		lifted = true

		return insertAction(ctx, tx, a, nil)
	})

	return lifted, rooms, err
}

// _activeRestrictions keeps, per user, the latest restricting action of the channel when it is still in force.
const _activeRestrictions = `WITH last AS (
		SELECT DISTINCT ON (a.target_user_id) a.id, a.target_user_id, a.type, a.expires_at, a.actor_id, a.reason, a.created_at
		FROM mod_actions a
		WHERE a.channel_id = $1 AND a.type IN ('BAN', 'UNBAN', 'TIMEOUT', 'UNTIMEOUT')
		ORDER BY a.target_user_id, a.created_at DESC, a.id DESC
	)
	SELECT l.id::text, l.target_user_id::text, COALESCE(tu.username, ''), COALESCE(tu.display_name, ''), l.type, l.expires_at,
	       l.actor_id::text, COALESCE(au.username, ''), COALESCE(au.display_name, ''), l.reason, l.created_at
	FROM last l LEFT JOIN users tu ON tu.id = l.target_user_id LEFT JOIN users au ON au.id = l.actor_id
	WHERE (l.type = 'BAN' OR (l.type = 'TIMEOUT' AND l.expires_at > now()))`

func scanRestriction(row pgx.CollectableRow) (entity.ActiveRestriction, error) {
	var (
		x   entity.ActiveRestriction
		typ string
	)

	err := row.Scan(&x.ID, &x.User.ID, &x.User.Username, &x.User.DisplayName, &typ, &x.Until,
		&x.Actor.ID, &x.Actor.Username, &x.Actor.DisplayName, &x.Reason, &x.CreatedAt)
	x.Type = entity.RestrictionType(typ)

	return x, err
}

// Restrictions -.
func (r *Repo) Restrictions(ctx context.Context, channelID string, t *entity.RestrictionType, after *repo.Cursor, limit int,
) ([]entity.ActiveRestriction, error) {
	q := _activeRestrictions
	args := []any{channelID, limit}

	if t != nil {
		args = append(args, string(*t))
		q += fmt.Sprintf(` AND l.type = $%d`, len(args))
	}

	if after != nil {
		args = append(args, after.CreatedAt, after.ID)
		q += fmt.Sprintf(` AND (l.created_at, l.id) < ($%d, $%d::uuid)`, len(args)-1, len(args))
	}

	rows, err := r.Pool.Query(ctx, q+` ORDER BY l.created_at DESC, l.id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - Restrictions: %w", err)
	}

	out, err := pgx.CollectRows(rows, scanRestriction)
	if err != nil {
		return nil, fmt.Errorf("Repo - Restrictions: %w", err)
	}

	return out, nil
}

// Restriction -.
func (r *Repo) Restriction(ctx context.Context, channelID, userID string) (*entity.ActiveRestriction, error) {
	rows, err := r.Pool.Query(ctx, _activeRestrictions+` AND l.target_user_id = $2`, channelID, userID)
	if err != nil {
		return nil, fmt.Errorf("Repo - Restriction: %w", err)
	}

	list, err := pgx.CollectRows(rows, scanRestriction)
	if err != nil {
		return nil, fmt.Errorf("Repo - Restriction: %w", err)
	}

	if len(list) == 0 {
		return nil, nil //nolint:nilnil // no restriction in force
	}

	return &list[0], nil
}

// Pinned -.
func (r *Repo) Pinned(ctx context.Context, streamID string) (*entity.Message, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_messageCols+`
		FROM chat_messages m LEFT JOIN users u ON u.id = m.user_id
		WHERE m.stream_id = $1 AND m.pinned AND m.deleted_at IS NULL LIMIT 1`, streamID)
	if err != nil {
		return nil, fmt.Errorf("Repo - Pinned: %w", err)
	}

	list, err := pgx.CollectRows(rows, scanMessage)
	if err != nil {
		return nil, fmt.Errorf("Repo - Pinned: %w", err)
	}

	if len(list) == 0 {
		return nil, nil //nolint:nilnil // nothing pinned
	}

	return &list[0], nil
}

// Evidence -.
func (r *Repo) Evidence(ctx context.Context, streamID string, messageID, userID *string, limit int) ([]entity.EvidenceItem, error) {
	var exists bool

	if err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chat_rooms WHERE stream_id = $1)`, streamID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("Repo - Evidence - room: %w", err)
	}

	if !exists {
		return nil, entity.ErrNotFound
	}

	const cols = `m.id::text, m.user_id::text, m.body, m.fragments, m.created_at, m.deleted_at IS NOT NULL`

	var (
		rows pgx.Rows
		err  error
	)

	if messageID != nil {
		from, to := createdRange(*messageID)
		rows, err = r.Pool.Query(ctx,
			`WITH anchor AS (SELECT user_id, created_at FROM chat_messages WHERE id = $2 AND stream_id = $1 AND created_at BETWEEN $4 AND $5)
			 (SELECT `+cols+` FROM chat_messages m, anchor a WHERE m.id = $2 AND m.stream_id = $1 AND m.created_at = a.created_at)
			 UNION ALL
			 (SELECT `+cols+` FROM chat_messages m, anchor a
			  WHERE m.stream_id = $1 AND m.user_id = a.user_id AND m.created_at < a.created_at
			  ORDER BY m.created_at DESC LIMIT $3)`, streamID, *messageID, limit-1, from, to)
	} else {
		rows, err = r.Pool.Query(ctx,
			`SELECT `+cols+` FROM chat_messages m WHERE m.stream_id = $1 AND m.user_id = $2 ORDER BY m.created_at DESC LIMIT $3`,
			streamID, derefOr(userID, ""), limit)
	}

	if err != nil {
		return nil, fmt.Errorf("Repo - Evidence: %w", err)
	}

	items, err := pgx.CollectRows(rows, scanEvidence)
	if err != nil {
		return nil, fmt.Errorf("Repo - Evidence: %w", err)
	}

	if messageID != nil && len(items) == 0 {
		return nil, entity.ErrNotFound
	}

	slices.SortFunc(items, func(a, b entity.EvidenceItem) int { return a.CreatedAt.Compare(b.CreatedAt) })

	return items, nil
}

func scanEvidence(row pgx.CollectableRow) (entity.EvidenceItem, error) {
	var (
		e     entity.EvidenceItem
		frags []byte
	)

	if err := row.Scan(&e.MessageID, &e.UserID, &e.Body, &frags, &e.CreatedAt, &e.Deleted); err != nil {
		return entity.EvidenceItem{}, fmt.Errorf("scan evidence: %w", err)
	}

	if err := json.Unmarshal(frags, &e.Fragments); err != nil || e.Fragments == nil {
		e.Fragments = []entity.Fragment{}
	}

	e.CreatedAt = e.CreatedAt.UTC()

	return e, nil
}

// ---- blocked terms -------------------------------------------------------------------------------

const _termCols = `t.id::text, t.channel_id::text, t.term, t.display_term, t.created_by::text, COALESCE(u.username, ''),
	COALESCE(u.display_name, ''), t.created_at`

func scanTerm(row pgx.CollectableRow) (entity.BlockedTerm, error) {
	var t entity.BlockedTerm

	err := row.Scan(&t.ID, &t.ChannelID, &t.Term, &t.Display, &t.CreatedBy.ID, &t.CreatedBy.Username, &t.CreatedBy.DisplayName,
		&t.CreatedAt)

	return t, err
}

// Terms -.
func (r *Repo) Terms(ctx context.Context, channelID string) ([]entity.BlockedTerm, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+_termCols+` FROM blocked_terms t LEFT JOIN users u ON u.id = t.created_by
		WHERE t.channel_id = $1 ORDER BY t.created_at DESC, t.id DESC`, channelID)
	if err != nil {
		return nil, fmt.Errorf("Repo - Terms: %w", err)
	}

	out, err := pgx.CollectRows(rows, scanTerm)
	if err != nil {
		return nil, fmt.Errorf("Repo - Terms: %w", err)
	}

	return out, nil
}

// AddTerm -.
func (r *Repo) AddTerm(ctx context.Context, t entity.BlockedTerm, maxTerms int, audit events.AuditRecorded) (entity.BlockedTerm, bool, error) {
	var (
		out     entity.BlockedTerm
		created bool
	)

	err := r.inTx(ctx, "Repo - AddTerm", func(tx pgx.Tx) error {
		if err := lockChannel(ctx, tx, t.ChannelID); err != nil {
			return err
		}

		var id string

		err := tx.QueryRow(ctx, `SELECT id::text FROM blocked_terms WHERE channel_id = $1 AND term = $2`, t.ChannelID, t.Term).Scan(&id)

		switch {
		case err == nil:
			out, err = termByID(ctx, tx, id)

			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("Repo - AddTerm - existing: %w", err)
		}

		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM blocked_terms WHERE channel_id = $1`, t.ChannelID).Scan(&n); err != nil {
			return fmt.Errorf("Repo - AddTerm - count: %w", err)
		}

		if n >= maxTerms {
			return entity.ErrLimitReached
		}

		if err = tx.QueryRow(ctx,
			`INSERT INTO blocked_terms (channel_id, term, display_term, created_by) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			t.ChannelID, t.Term, t.Display, t.CreatedBy.ID).Scan(&id); err != nil {
			return fmt.Errorf("Repo - AddTerm - insert: %w", err)
		}

		created = true

		if out, err = termByID(ctx, tx, id); err != nil {
			return err
		}

		return addOutboxAs(ctx, tx, _aggregateChannel, t.ChannelID, events.TypeAuditRecorded, audit)
	})

	return out, created, err
}

func termByID(ctx context.Context, tx pgx.Tx, id string) (entity.BlockedTerm, error) {
	rows, err := tx.Query(ctx, `SELECT `+_termCols+` FROM blocked_terms t LEFT JOIN users u ON u.id = t.created_by WHERE t.id = $1`, id)
	if err != nil {
		return entity.BlockedTerm{}, fmt.Errorf("term: %w", err)
	}

	t, err := pgx.CollectExactlyOneRow(rows, scanTerm)
	if err != nil {
		return entity.BlockedTerm{}, fmt.Errorf("term: %w", err)
	}

	return t, nil
}

// RemoveTerm -.
func (r *Repo) RemoveTerm(ctx context.Context, channelID, termID string, audit events.AuditRecorded) (bool, error) {
	removed := false

	err := r.inTx(ctx, "Repo - RemoveTerm", func(tx pgx.Tx) error {
		var display string

		err := tx.QueryRow(ctx, `DELETE FROM blocked_terms WHERE id = $1 AND channel_id = $2 RETURNING display_term`,
			termID, channelID).Scan(&display)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("Repo - RemoveTerm: %w", err)
		}

		removed = true

		if audit.Metadata == nil {
			audit.Metadata = map[string]any{}
		}

		audit.Metadata["term"] = display

		return addOutboxAs(ctx, tx, _aggregateChannel, channelID, events.TypeAuditRecorded, audit)
	})

	return removed, err
}
