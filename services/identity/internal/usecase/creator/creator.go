// Package creator implements creator applications and their review by admins.
package creator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/minhnn/nexuslive-be/services/identity/internal/repo"
	"github.com/minhnn/nexuslive-be/services/identity/internal/usecase"
)

const (
	_defaultMinAge = 18
	_statusAll     = "ALL"
)

// Config -.
type Config struct {
	// RequireVerifiedEmail refuses applications from accounts without a verified e-mail (BR-D05).
	RequireVerifiedEmail bool
	MinAge               int
	// Location decides which calendar day is "today" for ages (nil = UTC).
	Location *time.Location
}

// UseCase -.
type UseCase struct {
	apps     repo.ApplicationRepo
	users    repo.UserRepo
	channels repo.ChannelRepo
	cfg      Config
	now      func() time.Time
	l        logger.Interface
}

var (
	_ usecase.Creator     = (*UseCase)(nil)
	_ usecase.ChannelSync = (*UseCase)(nil)
)

// New -.
func New(apps repo.ApplicationRepo, users repo.UserRepo, channels repo.ChannelRepo, cfg Config, l logger.Interface) *UseCase {
	if cfg.MinAge <= 0 {
		cfg.MinAge = _defaultMinAge
	}

	if cfg.Location == nil {
		cfg.Location = time.UTC
	}

	return &UseCase{
		apps: apps, users: users, channels: channels, cfg: cfg,
		now: func() time.Time { return time.Now().UTC() }, l: l,
	}
}

// WithClock replaces the time source; used by tests.
func (uc *UseCase) WithClock(now func() time.Time) *UseCase {
	uc.now = now

	return uc
}

func (uc *UseCase) today() time.Time {
	return uc.now().In(uc.cfg.Location)
}

// Submit -.
func (uc *UseCase) Submit(ctx context.Context, in usecase.SubmitInput) (entity.Application, error) {
	today := uc.today()

	birth, err := entity.ParseBirthDate(in.BirthDate, today)
	if err != nil {
		return entity.Application{}, err
	}

	if err := uc.canSubmit(ctx, in.UserID); err != nil {
		return entity.Application{}, err
	}

	if entity.AgeOn(birth, today) < uc.cfg.MinAge {
		return entity.Application{}, entity.ErrUnderage
	}

	id, err := uuid.NewV7()
	if err != nil {
		return entity.Application{}, fmt.Errorf("CreatorUseCase - Submit - uuid.NewV7: %w", err)
	}

	now := uc.now()
	a := entity.Application{
		ID:        id.String(),
		UserID:    in.UserID,
		Status:    entity.ApplicationPending,
		LegalName: strings.TrimSpace(in.LegalName),
		BirthDate: birth,
		Note:      strings.TrimSpace(in.Note),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err = uc.apps.Create(ctx, a); err != nil {
		return entity.Application{}, fmt.Errorf("CreatorUseCase - Submit - uc.apps.Create: %w", err)
	}

	return a, nil
}

// canSubmit applies BR-D05 (verified e-mail) and BR-D11 (one pending application, one channel).
func (uc *UseCase) canSubmit(ctx context.Context, userID string) error {
	user, err := uc.users.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("CreatorUseCase - canSubmit - uc.users.GetByID: %w", err)
	}

	if uc.cfg.RequireVerifiedEmail && !user.IsEmailVerified() {
		return entity.ErrEmailNotVerified
	}

	state, err := uc.apps.CreatorState(ctx, userID)
	if err != nil {
		return fmt.Errorf("CreatorUseCase - canSubmit - uc.apps.CreatorState: %w", err)
	}

	switch {
	case state.HasChannel || state.HasApproved || user.IsCreator():
		return entity.ErrAlreadyCreator
	case state.HasPending:
		return entity.ErrApplicationPending
	default:
		return nil
	}
}

// Mine -.
func (uc *UseCase) Mine(ctx context.Context, userID string) (*entity.Application, error) {
	a, err := uc.apps.Latest(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("CreatorUseCase - Mine - uc.apps.Latest: %w", err)
	}

	return a, nil
}

// Cancel -.
func (uc *UseCase) Cancel(ctx context.Context, userID, id string) error {
	if err := uc.apps.Cancel(ctx, userID, id, uc.now()); err != nil {
		return fmt.Errorf("CreatorUseCase - Cancel - uc.apps.Cancel: %w", err)
	}

	return nil
}

// cursor is the opaque keyset position handed to admin clients.
type cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"id"`
}

// List -.
func (uc *UseCase) List(ctx context.Context, status, cur string, limit int) (usecase.ApplicationPage, error) {
	st := entity.ApplicationStatus(status)
	if status == _statusAll {
		st = ""
	}

	after, err := decodeCursor(cur)
	if err != nil {
		return usecase.ApplicationPage{}, err
	}

	// One extra row tells whether another page exists.
	items, err := uc.apps.List(ctx, st, after, limit+1)
	if err != nil {
		return usecase.ApplicationPage{}, fmt.Errorf("CreatorUseCase - List - uc.apps.List: %w", err)
	}

	page := usecase.ApplicationPage{Items: items}

	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]

		page.NextCursor, err = encodeCursor(cursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return usecase.ApplicationPage{}, err
		}
	}

	today := uc.today()
	for i := range page.Items {
		page.Items[i].Age = entity.AgeOn(page.Items[i].BirthDate, today)
	}

	return page, nil
}

func decodeCursor(s string) (*repo.ApplicationCursor, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // no cursor means the first page
	}

	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, entity.ErrInvalidCursor
	}

	var c cursor
	if err = json.Unmarshal(raw, &c); err != nil || c.CreatedAt.IsZero() {
		return nil, entity.ErrInvalidCursor
	}

	if _, err = uuid.Parse(c.ID); err != nil {
		return nil, entity.ErrInvalidCursor
	}

	return &repo.ApplicationCursor{CreatedAt: c.CreatedAt, ID: c.ID}, nil
}

func encodeCursor(c cursor) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("CreatorUseCase - encodeCursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Get -.
func (uc *UseCase) Get(ctx context.Context, id string) (entity.Application, error) {
	a, err := uc.apps.Get(ctx, id)
	if err != nil {
		return entity.Application{}, fmt.Errorf("CreatorUseCase - Get - uc.apps.Get: %w", err)
	}

	a.Age = entity.AgeOn(a.BirthDate, uc.today())

	return a, nil
}

// Approve -.
func (uc *UseCase) Approve(ctx context.Context, adminID, id string) (entity.Application, error) {
	if err := uc.checkReviewer(ctx, adminID, id); err != nil {
		return entity.Application{}, err
	}

	if err := uc.apps.Approve(ctx, adminID, id, uc.now()); err != nil {
		return entity.Application{}, fmt.Errorf("CreatorUseCase - Approve - uc.apps.Approve: %w", err)
	}

	uc.l.Info("audit=creator_approved adminID=%s applicationID=%s", adminID, id)

	return uc.Get(ctx, id)
}

// Reject -.
func (uc *UseCase) Reject(ctx context.Context, adminID, id, reason string) (entity.Application, error) {
	if err := uc.checkReviewer(ctx, adminID, id); err != nil {
		return entity.Application{}, err
	}

	if err := uc.apps.Reject(ctx, adminID, id, strings.TrimSpace(reason), uc.now()); err != nil {
		return entity.Application{}, fmt.Errorf("CreatorUseCase - Reject - uc.apps.Reject: %w", err)
	}

	uc.l.Info("audit=creator_rejected adminID=%s applicationID=%s", adminID, id)

	return uc.Get(ctx, id)
}

// checkReviewer enforces BR-D24 and the PENDING precondition; the repository re-checks the state
// atomically, so two admins reviewing at once still yield one winner.
func (uc *UseCase) checkReviewer(ctx context.Context, adminID, id string) error {
	a, err := uc.apps.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("CreatorUseCase - checkReviewer - uc.apps.Get: %w", err)
	}

	if a.UserID == adminID {
		return entity.ErrSelfReview
	}

	if a.Status != entity.ApplicationPending {
		return entity.ErrInvalidState
	}

	return nil
}

// ChannelCreated -.
func (uc *UseCase) ChannelCreated(ctx context.Context, eventID string, ch entity.UserChannel) error {
	if err := uc.channels.UpsertChannel(ctx, eventID, ch); err != nil {
		return fmt.Errorf("CreatorUseCase - ChannelCreated - uc.channels.UpsertChannel: %w", err)
	}

	return nil
}
