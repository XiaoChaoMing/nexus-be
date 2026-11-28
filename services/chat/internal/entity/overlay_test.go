package entity_test

import (
	"testing"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/stretchr/testify/require"
)

const overlayChannel = "0199d4a2-0000-7000-8000-0000000000c1"

func TestDefaultOverlaySettingsAreValid(t *testing.T) {
	t.Parallel()

	s := entity.DefaultOverlaySettings(overlayChannel)

	require.NoError(t, s.Validate())
	require.Equal(t, overlayChannel, s.ChannelID)
	require.Nil(t, s.UpdatedAt, "a channel that never saved has no save time")
	require.Equal(t, entity.OverlayBackgroundTransparent, s.Chat.Background)
}

func TestOverlaySettingsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*entity.OverlaySettings)
		want   map[string]string
	}{
		{"unknown preset", func(s *entity.OverlaySettings) { s.Chat.Preset = "RAINBOW" }, map[string]string{"chat.preset": entity.DetailEnum}},
		{"unknown font", func(s *entity.OverlaySettings) { s.Chat.FontFamily = "COMIC" }, map[string]string{"chat.fontFamily": entity.DetailEnum}},
		{"short colour", func(s *entity.OverlaySettings) { s.Chat.TextColor = "#FFF" }, map[string]string{"chat.textColor": entity.DetailFormat}},
		{"css injection in colour", func(s *entity.OverlaySettings) { s.Alert.AccentColor = "red;background:url(x)" }, map[string]string{"alert.accentColor": entity.DetailFormat}},
		{"opacity over 100", func(s *entity.OverlaySettings) { s.Chat.BgOpacity = 101 }, map[string]string{"chat.bgOpacity": entity.DetailLength}},
		{"font too small", func(s *entity.OverlaySettings) { s.Chat.FontSize = 11 }, map[string]string{"chat.fontSize": entity.DetailLength}},
		{"too many messages", func(s *entity.OverlaySettings) { s.Chat.MaxMessages = 31 }, map[string]string{"chat.maxMessages": entity.DetailLength}},
		{"fade under 5 s", func(s *entity.OverlaySettings) { s.Chat.FadeAfterSec = 3 }, map[string]string{"chat.fadeAfterSec": entity.DetailLength}},
		{"alert too short", func(s *entity.OverlaySettings) { s.Alert.DurationSec = 2 }, map[string]string{"alert.durationSec": entity.DetailLength}},
		{"negative minimum", func(s *entity.OverlaySettings) { s.Alert.MinTotal = -1 }, map[string]string{"alert.minTotal": entity.DetailLength}},
		{"unknown animation", func(s *entity.OverlaySettings) { s.Alert.Animation = "SPIN" }, map[string]string{"alert.animation": entity.DetailEnum}},
		{
			"several at once",
			func(s *entity.OverlaySettings) { s.Chat.Preset, s.Alert.Position = "", "LEFT" },
			map[string]string{"chat.preset": entity.DetailEnum, "alert.position": entity.DetailEnum},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := entity.DefaultOverlaySettings(overlayChannel)
			tc.mutate(&s)

			err := s.Validate()
			require.ErrorIs(t, err, entity.ErrValidation)
			require.Equal(t, tc.want, entity.DetailsOf(err))
		})
	}
}

func TestOverlaySettingsAcceptEdgeValues(t *testing.T) {
	t.Parallel()

	s := entity.DefaultOverlaySettings(overlayChannel)
	s.Chat.BgOpacity, s.Chat.FontSize, s.Chat.MaxMessages, s.Chat.FadeAfterSec = 0, entity.OverlayMaxFontSize, entity.OverlayMinMessages, entity.OverlayMaxFadeSec
	s.Alert.DurationSec, s.Alert.MinTotal = entity.OverlayMinDurationSec, entity.OverlayMaxMinTotal
	s.Chat.TextColor = "#aBcDeF"

	require.NoError(t, s.Validate())
}

func TestOverlayTopic(t *testing.T) {
	t.Parallel()

	topic, err := entity.ParseTopic("overlay:" + overlayChannel)
	require.NoError(t, err)
	require.Equal(t, entity.OverlayTopic(overlayChannel), topic)
	require.Empty(t, topic.StreamID(), "an overlay topic is not a stream: no viewer is counted")
}
