// Package moderation implements user blocks, the account actions of safety-svc, the admin's view of an
// account and the snapshot other services backfill their copies from.
package moderation

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/events"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

const (
	_maxPage     = 50
	_defaultPage = 20
	_maxSnapshot = 500
)

// Repos groups the storage the use case needs.
type Repos struct {
	Users    repo.UserRepo
	Blocks   repo.BlockRepo
	Actions  repo.ModerationRepo
	Snapshot repo.SnapshotRepo
}

// UseCase implements usecase.Blocks, usecase.Moderation, usecase.AdminUsers and usecase.Snapshot.
type UseCase struct {
	r   Repos
	l   logger.Interface
	now func() time.Time
}

var (
	_ usecase.Blocks     = (*UseCase)(nil)
	_ usecase.Moderation = (*UseCase)(nil)
	_ usecase.AdminUsers = (*UseCase)(nil)
	_ usecase.Snapshot   = (*UseCase)(nil)
)

// New -.
func New(r Repos, l logger.Interface) *UseCase {
	return &UseCase{r: r, l: l, now: time.Now}
}

// WithClock -.
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

// List -.
func (uc *UseCase) List(ctx context.Context, userID, cursor string, limit int) (usecase.BlockPage, error) {
	after, err := decodeBlockCursor(cursor)
	if err != nil {
		return usecase.BlockPage{}, err
	}

	limit = pageSize(limit)

	items, err := uc.r.Blocks.List(ctx, userID, after, limit+1)
	if err != nil {
		return usecase.BlockPage{}, fmt.Errorf("ModerationUseCase - List: %w", err)
	}

	page := usecase.BlockPage{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeBlockCursor(repo.BlockCursor{CreatedAt: last.CreatedAt, UserID: last.UserID})
	}

	return page, nil
}

// IDs -.
func (uc *UseCase) IDs(ctx context.Context, userID string) ([]string, error) {
	ids, err := uc.r.Blocks.IDs(ctx, userID, entity.BlockMax)
	if err != nil {
		return nil, fmt.Errorf("ModerationUseCase - IDs: %w", err)
	}

	return ids, nil
}

// Block -.
func (uc *UseCase) Block(ctx context.Context, userID, targetID string) error {
	if userID == targetID {
		return entity.ErrSelfBlock
	}

	added, err := uc.r.Blocks.Add(ctx, userID, targetID, entity.BlockMax)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) || errors.Is(err, entity.ErrBlockLimit) {
			return err
		}

		return fmt.Errorf("ModerationUseCase - Block: %w", err)
	}

	if added {
		uc.l.Info("audit=user_blocked blocker=%s blocked=%s", userID, targetID)
	}

	return nil
}

// Unblock -.
func (uc *UseCase) Unblock(ctx context.Context, userID, targetID string) error {
	if _, err := uc.r.Blocks.Remove(ctx, userID, targetID); err != nil {
		return fmt.Errorf("ModerationUseCase - Unblock: %w", err)
	}

	return nil
}

// Apply locks or unlocks the account; a target that cannot change (admin, deleted, not locked) is acked.
func (uc *UseCase) Apply(ctx context.Context, eventID string, e events.UserActioned) error {
	changed, err := uc.r.Actions.ApplyUserAction(ctx, eventID, e)
	if err != nil {
		return fmt.Errorf("ModerationUseCase - Apply: %w", err)
	}

	if !changed {
		uc.l.Warn("identity - %s of %s (action %s) changed nothing", e.Action, e.UserID, e.ActionID)

		return nil
	}

	uc.l.Info("audit=user_%s userID=%s action=%s admin=%s", strings.ToLower(e.Action), e.UserID, e.ActionID, e.AdminID)

	return nil
}

// Get -.
func (uc *UseCase) Get(ctx context.Context, id string) (entity.User, error) {
	u, err := uc.r.Users.GetByID(ctx, id)
	if errors.Is(err, entity.ErrUserNotFound) {
		return entity.User{}, entity.ErrNotFound
	}

	if err != nil {
		return entity.User{}, fmt.Errorf("ModerationUseCase - Get: %w", err)
	}

	return u.Effective(uc.now()), nil
}

// Users -.
func (uc *UseCase) Users(ctx context.Context, after string, limit int) ([]entity.User, string, error) {
	if after != "" {
		if _, err := uuid.Parse(after); err != nil {
			return nil, "", entity.ErrInvalidCursor
		}
	}

	limit = snapshotSize(limit)

	users, err := uc.r.Snapshot.Users(ctx, after, limit)
	if err != nil {
		return nil, "", fmt.Errorf("ModerationUseCase - Users: %w", err)
	}

	next := ""
	if len(users) == limit {
		next = users[limit-1].ID
	}

	return users, next, nil
}

// Blocks -.
func (uc *UseCase) Blocks(ctx context.Context, after string, limit int) ([]entity.BlockPair, string, error) {
	var pos *entity.BlockPair

	if after != "" {
		blocker, blocked, ok := strings.Cut(after, ":")
		if !ok || uuid.Validate(blocker) != nil || uuid.Validate(blocked) != nil {
			return nil, "", entity.ErrInvalidCursor
		}

		pos = &entity.BlockPair{BlockerID: blocker, BlockedID: blocked}
	}

	limit = snapshotSize(limit)

	rows, err := uc.r.Snapshot.Blocks(ctx, pos, limit)
	if err != nil {
		return nil, "", fmt.Errorf("ModerationUseCase - Blocks: %w", err)
	}

	next := ""
	if len(rows) == limit {
		next = rows[limit-1].BlockerID + ":" + rows[limit-1].BlockedID
	}

	return rows, next, nil
}

func pageSize(limit int) int {
	if limit <= 0 || limit > _maxPage {
		return _defaultPage
	}

	return limit
}

func snapshotSize(limit int) int {
	if limit <= 0 || limit > _maxSnapshot {
		return _maxSnapshot
	}

	return limit
}

func encodeBlockCursor(c repo.BlockCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.UserID))
}

func decodeBlockCursor(s string) (*repo.BlockCursor, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // no cursor: first page
	}

	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, entity.ErrInvalidCursor
	}

	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || uuid.Validate(id) != nil {
		return nil, entity.ErrInvalidCursor
	}

	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, entity.ErrInvalidCursor
	}

	return &repo.BlockCursor{CreatedAt: t, UserID: id}, nil
}
