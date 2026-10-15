package notify

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

const _cursorSep = "|"

// Actions of Observer.Read.
const (
	ActionRead    = "read"
	ActionReadAll = "read_all"
	ActionDelete  = "delete"
)

// List pages the caller's notifications, most recently updated first (keyset (updated_at, id)).
func (uc *UseCase) List(ctx context.Context, userID string, unreadOnly bool, cursor string, limit int) (usecase.Page, error) {
	if limit <= 0 {
		limit = _defaultLimit
	}

	limit = min(limit, _maxLimit)

	var after *repo.Cursor

	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return usecase.Page{}, err
		}

		after = &c
	}

	items, err := uc.r.Notifications.List(ctx, userID, unreadOnly, after, limit+1)
	if err != nil {
		return usecase.Page{}, fmt.Errorf("notify - List: %w", err)
	}

	page := usecase.Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = ptr(encodeCursor(repo.Cursor{UpdatedAt: last.UpdatedAt, ID: last.ID}))
	}

	return page, nil
}

func encodeCursor(c repo.Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.UpdatedAt.UTC().Format(time.RFC3339Nano) + _cursorSep + c.ID))
}

func decodeCursor(s string) (repo.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return repo.Cursor{}, entity.ErrBadCursor
	}

	at, id, ok := strings.Cut(string(raw), _cursorSep)
	if !ok || uuid.Validate(id) != nil {
		return repo.Cursor{}, entity.ErrBadCursor
	}

	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return repo.Cursor{}, entity.ErrBadCursor
	}

	return repo.Cursor{UpdatedAt: t, ID: id}, nil
}

// UnreadCount -.
func (uc *UseCase) UnreadCount(ctx context.Context, userID string) (int, error) {
	n, err := uc.r.Notifications.UnreadCount(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("notify - UnreadCount: %w", err)
	}

	return n, nil
}

// Read marks one notification read; already read is fine, another user's is not found.
func (uc *UseCase) Read(ctx context.Context, userID, id string) error {
	if uuid.Validate(id) != nil {
		return entity.ErrNotFound
	}

	if err := uc.r.Notifications.MarkRead(ctx, userID, id); err != nil {
		return fmt.Errorf("notify - Read: %w", err)
	}

	uc.obs.Read(ActionRead)

	return nil
}

// ReadAll marks read what was updated at or before before (BR-E19).
func (uc *UseCase) ReadAll(ctx context.Context, userID string, before time.Time) error {
	if before.IsZero() {
		before = uc.now()
	}

	if _, err := uc.r.Notifications.MarkAllRead(ctx, userID, before); err != nil {
		return fmt.Errorf("notify - ReadAll: %w", err)
	}

	uc.obs.Read(ActionReadAll)

	return nil
}

// Delete soft-deletes one notification; already deleted is fine, another user's is not found.
func (uc *UseCase) Delete(ctx context.Context, userID, id string) error {
	if uuid.Validate(id) != nil {
		return entity.ErrNotFound
	}

	if err := uc.r.Notifications.SoftDelete(ctx, userID, id); err != nil {
		return fmt.Errorf("notify - Delete: %w", err)
	}

	uc.obs.Read(ActionDelete)

	return nil
}
