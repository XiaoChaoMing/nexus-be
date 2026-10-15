package notify_test

import (
	"context"
	"testing"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
	"github.com/stretchr/testify/require"
)

func b(v bool) *bool { return &v }

func TestSettingsDefaults(t *testing.T) {
	t.Parallel()

	e := newEnv()
	e.store.addUsers(recipient(viewerID, true))

	v, err := e.uc.Get(context.Background(), viewerID)
	require.NoError(t, err)
	require.True(t, v.EmailVerified)
	require.Len(t, v.Items, 8)

	got := map[entity.Type][2]bool{}
	for _, it := range v.Items {
		got[it.Setting.Type] = [2]bool{it.Setting.InApp, it.Setting.Email}
	}

	// BR-E02.
	require.Equal(t, map[entity.Type][2]bool{
		entity.TypeCreatorLive: {true, true}, entity.TypeStreamStarting: {true, true}, entity.TypeSchedule: {true, true},
		entity.TypeMention: {true, false}, entity.TypeNewFollower: {true, false}, entity.TypeCreatorApplication: {true, true},
		entity.TypeReportResult: {true, false}, entity.TypeSystem: {true, true},
	}, got)
	require.Equal(t, entity.TypeCreatorLive, v.Items[0].Setting.Type, "order of BR-E01")
	require.False(t, v.Items[4].Default.EmailAvailable, "NEW_FOLLOWER has no e-mail")
	require.True(t, v.Items[7].Default.InAppLocked)
}

func TestSettingsUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		items   []usecase.SettingPatch
		err     error
		details map[string]string
	}{
		{name: "turn off live e-mail", items: []usecase.SettingPatch{{Type: "CREATOR_LIVE", Email: b(false)}}},
		{name: "no rows", err: entity.ErrValidation, details: map[string]string{"items": "REQUIRED"}},
		{
			name: "unknown type", items: []usecase.SettingPatch{{Type: "VOD", InApp: b(false)}},
			err: entity.ErrValidation, details: map[string]string{"type": "ENUM"},
		},
		{
			name: "SYSTEM in-app off", items: []usecase.SettingPatch{{Type: "SYSTEM", InApp: b(false)}},
			err: entity.ErrLockedType, details: map[string]string{"SYSTEM": "INVALID"},
		},
		{
			name: "CREATOR_APPLICATION e-mail off", items: []usecase.SettingPatch{{Type: "CREATOR_APPLICATION", Email: b(false)}},
			err: entity.ErrLockedType, details: map[string]string{"CREATOR_APPLICATION": "INVALID"},
		},
		{
			name: "NEW_FOLLOWER e-mail on", items: []usecase.SettingPatch{{Type: "NEW_FOLLOWER", Email: b(true)}},
			err: entity.ErrLockedType, details: map[string]string{"NEW_FOLLOWER": "INVALID"},
		},
		{name: "locked switch sent unchanged", items: []usecase.SettingPatch{{Type: "SYSTEM", InApp: b(true), Email: b(true)}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv()

			v, err := e.uc.Update(context.Background(), viewerID, tc.items)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, tc.details, entity.DetailsOf(err))
				require.Empty(t, e.store.saved)

				return
			}

			require.NoError(t, err)
			require.Len(t, v.Items, 8)
		})
	}
}

func TestUnsubscribe(t *testing.T) {
	t.Parallel()

	e := newEnv()

	token, ok := entity.UnsubscribeToken(secret, viewerID, entity.TypeCreatorLive)
	require.True(t, ok)

	got, err := e.uc.Unsubscribe(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, entity.TypeCreatorLive, got)
	require.Equal(t, entity.Setting{Type: entity.TypeCreatorLive, InApp: true, Email: false},
		e.store.overrides[viewerID][entity.TypeCreatorLive])

	// A type without e-mail or a locked one cannot be unsubscribed.
	for _, typ := range []entity.Type{entity.TypeSystem, entity.TypeNewFollower} {
		tok, _ := entity.UnsubscribeToken(secret, viewerID, typ)
		_, err := e.uc.Unsubscribe(context.Background(), tok)
		require.ErrorIs(t, err, entity.ErrTokenInvalid, typ)
	}
}
