package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

// Get returns the caller's settings in the order of BR-E01.
func (uc *UseCase) Get(ctx context.Context, userID string) (usecase.SettingsView, error) {
	overrides, err := uc.r.Settings.Overrides(ctx, []string{userID})
	if err != nil {
		return usecase.SettingsView{}, fmt.Errorf("notify - settings: %w", err)
	}

	found, err := uc.r.Recipients.Recipients(ctx, []string{userID})
	if err != nil {
		return usecase.SettingsView{}, fmt.Errorf("notify - settings - user: %w", err)
	}

	return view(overrides[userID], found[userID].EmailVerified), nil
}

func view(overrides map[entity.Type]entity.Setting, verified bool) usecase.SettingsView {
	v := usecase.SettingsView{EmailVerified: verified}

	for _, t := range entity.Types() {
		var o *entity.Setting
		if s, ok := overrides[t]; ok {
			o = &s
		}

		v.Items = append(v.Items, usecase.SettingView{Setting: entity.Effective(t, o), Default: entity.DefaultOf(t)})
	}

	return v
}

// Update applies the patches row by row (PUT semantics per row, NXL-00 §3.2); a locked switch turned off or
// e-mail turned on for NEW_FOLLOWER → ErrLockedType with details.<TYPE> = INVALID.
func (uc *UseCase) Update(ctx context.Context, userID string, items []usecase.SettingPatch) (usecase.SettingsView, error) {
	if len(items) == 0 || len(items) > len(entity.Types()) {
		return usecase.SettingsView{}, entity.Reject(entity.ErrValidation, "items", entity.DetailRequired)
	}

	overrides, err := uc.r.Settings.Overrides(ctx, []string{userID})
	if err != nil {
		return usecase.SettingsView{}, fmt.Errorf("notify - settings: %w", err)
	}

	current := overrides[userID]
	changed := make([]entity.Setting, 0, len(items))

	for _, p := range items {
		s, err := apply(p, current)
		if err != nil {
			return usecase.SettingsView{}, err
		}

		if current == nil {
			current = map[entity.Type]entity.Setting{}
		}

		current[s.Type] = s
		changed = append(changed, s)
	}

	if err := uc.r.Settings.Save(ctx, userID, changed); err != nil {
		return usecase.SettingsView{}, fmt.Errorf("notify - settings - save: %w", err)
	}

	uc.l.Info("notify - settings updated: userID=%s types=%d", userID, len(changed))

	return uc.Get(ctx, userID)
}

// apply validates one patch against the locks and returns the new effective setting of its type.
func apply(p usecase.SettingPatch, current map[entity.Type]entity.Setting) (entity.Setting, error) {
	t := entity.Type(p.Type)
	if !t.Valid() {
		return entity.Setting{}, entity.Reject(entity.ErrValidation, "type", entity.DetailEnum)
	}

	var o *entity.Setting
	if s, ok := current[t]; ok {
		o = &s
	}

	s := entity.Effective(t, o)
	d := entity.DefaultOf(t)
	locked := entity.Reject(entity.ErrLockedType, string(t), entity.DetailInvalid)

	if p.InApp != nil {
		if d.InAppLocked && *p.InApp != s.InApp {
			return s, locked
		}

		s.InApp = *p.InApp
	}

	if p.Email != nil {
		if d.EmailLocked && *p.Email != s.Email {
			return s, locked
		}

		s.Email = *p.Email
	}

	return s, nil
}

// Unsubscribe turns off the e-mails of the type a token names (BR-E10).
func (uc *UseCase) Unsubscribe(ctx context.Context, token string) (entity.Type, error) {
	userID, t, err := entity.ParseUnsubscribeToken(uc.cfg.UnsubscribeSecret, token)
	if err != nil {
		return "", err
	}

	d := entity.DefaultOf(t)
	if !d.EmailAvailable || d.EmailLocked {
		return "", entity.ErrTokenInvalid
	}

	off := false
	if _, err := uc.Update(ctx, userID, []usecase.SettingPatch{{Type: string(t), Email: &off}}); err != nil {
		if errors.Is(err, entity.ErrLockedType) {
			return "", entity.ErrTokenInvalid
		}

		return "", err
	}

	uc.l.Info("notify - unsubscribed: userID=%s type=%s", userID, t)

	return t, nil
}
