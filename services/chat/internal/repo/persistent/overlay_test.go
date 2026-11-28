package persistent_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestOverlaySettings(t *testing.T) { //nolint:paralleltest // shared database
	r, _ := setup(t)
	ctx := context.Background()
	channel, user := uuid.NewString(), uuid.NewString()

	got, err := r.Overlay(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, entity.DefaultOverlaySettings(channel), got, "never saved: the defaults, no save time")

	in := entity.DefaultOverlaySettings(channel)
	in.Chat.Preset = entity.OverlayPresetNeon
	in.Chat.TextColor = "#00FF88"
	in.Alert.MinTotal = 50_000
	in.UpdatedBy = &user

	saved, err := r.SaveOverlay(ctx, in)
	require.NoError(t, err)
	require.NotNil(t, saved.UpdatedAt)

	got, err = r.Overlay(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, in.Chat, got.Chat)
	require.Equal(t, in.Alert, got.Alert)
	require.Equal(t, user, *got.UpdatedBy)

	in.Chat.Preset = entity.OverlayPresetDark
	_, err = r.SaveOverlay(ctx, in)
	require.NoError(t, err, "a second save replaces the first")

	got, err = r.Overlay(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, entity.OverlayPresetDark, got.Chat.Preset)
}
