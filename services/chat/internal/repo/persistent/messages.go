package persistent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
)

const (
	_partitionPrefix = "chat_messages_"
	_partitionLayout = "20060102"
	_day             = 24 * time.Hour
	// Serializes partition maintenance of all chat-svc instances (pg_advisory_xact_lock key).
	_partitionLockKey = 0x6e786c63686174
)

var _partitionRe = regexp.MustCompile(`^chat_messages_(\d{8})$`)

// InsertMessage -.
func (r *Repo) InsertMessage(ctx context.Context, m entity.Message) (entity.Message, error) {
	frags, err := json.Marshal(m.Fragments)
	if err != nil {
		return entity.Message{}, fmt.Errorf("Repo - InsertMessage - fragments: %w", err)
	}

	const q = `INSERT INTO chat_messages (stream_id, user_id, client_msg_id, body, fragments, type)
	           VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text, created_at`

	mentioned := m.Mentioned()
	if len(mentioned) == 0 {
		if err = r.Pool.QueryRow(ctx, q, m.StreamID, m.User.ID, m.ClientMsgID, m.Body, frags, m.Type).Scan(&m.ID, &m.CreatedAt); err != nil {
			return entity.Message{}, fmt.Errorf("Repo - InsertMessage: %w", err)
		}

		m.CreatedAt = m.CreatedAt.UTC()

		return m, nil
	}

	err = r.inTx(ctx, "Repo - InsertMessage", func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, q, m.StreamID, m.User.ID, m.ClientMsgID, m.Body, frags, m.Type).Scan(&m.ID, &m.CreatedAt); err != nil {
			return fmt.Errorf("Repo - InsertMessage: %w", err)
		}

		var channelID string
		if err := tx.QueryRow(ctx, `SELECT channel_id::text FROM chat_rooms WHERE stream_id = $1`, m.StreamID).Scan(&channelID); err != nil {
			return fmt.Errorf("Repo - InsertMessage - room: %w", err)
		}

		return addOutbox(ctx, tx, m.StreamID, events.TypeChatMention, events.ChatMention{
			MessageID: m.ID, StreamID: m.StreamID, ChannelID: channelID, SenderID: m.User.ID, SenderUsername: m.User.Username,
			MentionedUserIDs: mentioned, Excerpt: m.Excerpt(),
		})
	})
	if err != nil {
		return entity.Message{}, err
	}

	m.CreatedAt = m.CreatedAt.UTC()

	return m, nil
}

// History -.
func (r *Repo) History(ctx context.Context, streamID string, before *string, limit int) ([]entity.Message, error) {
	q := `SELECT ` + _messageCols + `
	      FROM chat_messages m LEFT JOIN users u ON u.id = m.user_id
	      WHERE m.stream_id = $1`
	args := []any{streamID, limit}

	if before != nil {
		// The cursor is a message id; its (created_at, id) is the keyset position.
		q += ` AND (m.created_at, m.id) < (SELECT created_at, id FROM chat_messages WHERE id = $3 AND stream_id = $1)`

		args = append(args, *before)
	}

	q += ` ORDER BY m.created_at DESC, m.id DESC LIMIT $2`

	rows, err := r.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("Repo - History: %w", err)
	}

	out, err := pgx.CollectRows(rows, scanMessage)
	if err != nil {
		return nil, fmt.Errorf("Repo - History: %w", err)
	}

	return out, nil
}

// _messageCols is read by scanMessage (alias m, LEFT JOIN users u).
const _messageCols = `m.id::text, m.stream_id::text, m.user_id::text, m.body, m.fragments, m.type, m.created_at,
	COALESCE(u.username, ''), COALESCE(NULLIF(u.display_name, ''), u.username, ''), m.pinned, m.deleted_at IS NOT NULL`

func scanMessage(row pgx.CollectableRow) (entity.Message, error) {
	var (
		m     entity.Message
		frags []byte
	)

	if err := row.Scan(&m.ID, &m.StreamID, &m.User.ID, &m.Body, &frags, &m.Type, &m.CreatedAt,
		&m.User.Username, &m.User.DisplayName, &m.Pinned, &m.Deleted); err != nil {
		return entity.Message{}, fmt.Errorf("scan message: %w", err)
	}

	if err := json.Unmarshal(frags, &m.Fragments); err != nil {
		return entity.Message{}, fmt.Errorf("scan message fragments: %w", err)
	}

	if m.Fragments == nil {
		m.Fragments = []entity.Fragment{}
	}

	m.CreatedAt = m.CreatedAt.UTC()

	return m, nil
}

// EnsurePartitions -.
func (r *Repo) EnsurePartitions(ctx context.Context, from time.Time, days int) ([]string, error) {
	created := []string{}

	err := r.inTx(ctx, "Repo - EnsurePartitions", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, _partitionLockKey); err != nil {
			return fmt.Errorf("Repo - EnsurePartitions - lock: %w", err)
		}

		existing, err := partitions(ctx, tx)
		if err != nil {
			return err
		}

		for i := range days {
			day := from.UTC().Truncate(_day).Add(time.Duration(i) * _day)
			name := _partitionPrefix + day.Format(_partitionLayout)

			if existing[name] {
				continue
			}

			if cerr := createPartition(ctx, tx, name, day); cerr != nil {
				return cerr
			}

			created = append(created, name)
		}

		return nil
	})

	return created, err
}

// createPartition creates the day's table, moves that day's rows out of the DEFAULT partition (they
// would block the attach) and attaches it. DDL takes no bind parameters: the name and bounds are built
// from a formatted date only.
func createPartition(ctx context.Context, tx pgx.Tx, name string, day time.Time) error {
	from, to := day.Format(time.RFC3339), day.Add(_day).Format(time.RFC3339)
	ident := pgx.Identifier{name}.Sanitize()

	stmts := []string{
		`CREATE TABLE ` + ident + ` (LIKE chat_messages INCLUDING DEFAULTS INCLUDING CONSTRAINTS)`,
		`WITH moved AS (DELETE FROM chat_messages_default WHERE created_at >= '` + from + `' AND created_at < '` + to + `' RETURNING *)
		 INSERT INTO ` + ident + ` SELECT * FROM moved`,
		`ALTER TABLE chat_messages ATTACH PARTITION ` + ident + ` FOR VALUES FROM ('` + from + `') TO ('` + to + `')`,
	}

	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("Repo - createPartition %s: %w", name, err)
		}
	}

	return nil
}

// DropPartitionsBefore -.
func (r *Repo) DropPartitionsBefore(ctx context.Context, day time.Time) ([]string, error) {
	dropped := []string{}
	limit := day.UTC().Truncate(_day)

	err := r.inTx(ctx, "Repo - DropPartitionsBefore", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, _partitionLockKey); err != nil {
			return fmt.Errorf("Repo - DropPartitionsBefore - lock: %w", err)
		}

		existing, err := partitions(ctx, tx)
		if err != nil {
			return err
		}

		for name := range existing {
			if d, ok := partitionDay(name); !ok || !d.Before(limit) {
				continue
			}

			if derr := dropPartition(ctx, tx, name); derr != nil {
				return derr
			}

			dropped = append(dropped, name)
		}

		return nil
	})

	return dropped, err
}

func partitionDay(name string) (time.Time, bool) {
	m := _partitionRe.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}

	d, err := time.Parse(_partitionLayout, m[1])

	return d, err == nil
}

func dropPartition(ctx context.Context, tx pgx.Tx, name string) error {
	ident := pgx.Identifier{name}.Sanitize()

	if _, err := tx.Exec(ctx, `ALTER TABLE chat_messages DETACH PARTITION `+ident); err != nil {
		return fmt.Errorf("Repo - dropPartition - detach %s: %w", name, err)
	}

	if _, err := tx.Exec(ctx, `DROP TABLE `+ident); err != nil {
		return fmt.Errorf("Repo - dropPartition - drop %s: %w", name, err)
	}

	return nil
}

// partitions lists the daily partitions of chat_messages (the DEFAULT partition excluded).
func partitions(ctx context.Context, tx pgx.Tx) (map[string]bool, error) {
	rows, err := tx.Query(ctx,
		`SELECT c.relname FROM pg_inherits i
		 JOIN pg_class c ON c.oid = i.inhrelid
		 JOIN pg_class p ON p.oid = i.inhparent
		 WHERE p.relname = 'chat_messages'`)
	if err != nil {
		return nil, fmt.Errorf("Repo - partitions: %w", err)
	}

	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("Repo - partitions: %w", err)
	}

	out := map[string]bool{}

	for _, n := range names {
		if _partitionRe.MatchString(n) {
			out[n] = true
		}
	}

	return out, nil
}
