package entity_test

import (
	"strings"
	"testing"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestNormalizeBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "trims", in: "  xin chào  ", want: "xin chào"},
		{name: "NFC", in: "Việt", want: "Việt"},
		{name: "drops controls and bidi overrides", in: "a\u0007b\u202Ec\u200Bd", want: "abcd"},
		{name: "keeps the emoji joiner", in: "👩\u200D💻", want: "👩\u200D💻"},
		{name: "CRLF", in: "a\r\nb", want: "a\nb"},
		{name: "only spaces", in: " \n\t ", wantErr: entity.DetailRequired},
		{name: "500 runes ok", in: strings.Repeat("ạ", 500), want: strings.Repeat("ạ", 500)},
		{name: "501 runes", in: strings.Repeat("a", 501), wantErr: entity.DetailLength},
		{name: "three lines ok", in: "a\nb\nc", want: "a\nb\nc"},
		{name: "four lines", in: "a\nb\nc\nd", wantErr: entity.DetailLength},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := entity.NormalizeBody(tc.in, 3)
			if tc.wantErr != "" {
				require.ErrorIs(t, err, entity.ErrValidation)
				require.Equal(t, map[string]string{"body": tc.wantErr}, entity.DetailsOf(err))

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestBuildFragments(t *testing.T) {
	t.Parallel()

	emotes := map[string]entity.Emote{"nxlGG": {ID: "e1", Code: "nxlGG", ImageURL: "/emotes/nxlGG.svg"}}
	users := map[string]string{"minhnn": "u1"}

	got := entity.BuildFragments("gg nxlGG @MinhNN, hi @nobody nxlgg", emotes, users)
	require.Equal(t, []entity.Fragment{
		{Type: "text", Text: "gg "},
		{Type: "emote", Text: "nxlGG", Emote: &entity.EmoteRef{ID: "e1", Code: "nxlGG", ImageURL: "/emotes/nxlGG.svg"}},
		{Type: "text", Text: " "},
		{Type: "mention", Text: "@MinhNN", UserID: "u1"},
		{Type: "text", Text: ", hi @nobody nxlgg"},
	}, got)
	require.Equal(t, []string{"minhnn", "nobody"}, entity.MentionCandidates("@MinhNN, @nobody @minhnn x@y"))
}

func TestParseTopic(t *testing.T) {
	t.Parallel()

	id := "0199d4a2-0000-7000-8000-000000000001"

	for _, ok := range []string{"chat:" + id, "stream:" + id, "user:" + id, "chat:" + strings.ToUpper(id)} {
		tp, err := entity.ParseTopic(ok)
		require.NoError(t, err, ok)
		require.Equal(t, id, tp.ID)
	}

	for _, bad := range []string{"", "chat", "chat:x", "sidebar:" + id, "chat:" + id + "x", "chat:{" + id + "}"} {
		_, err := entity.ParseTopic(bad)
		require.ErrorIs(t, err, entity.ErrInvalidTopic, bad)
	}
}

func TestStandingAndViewerKey(t *testing.T) {
	t.Parallel()

	s := entity.Standing{Streamer: true, Moderator: true, VIP: true, SubTier: 2}
	require.Equal(t, []string{"STREAMER", "MODERATOR", "VIP"}, s.Badges())
	require.Equal(t, []string{"VIP", "SUB_T2"}, entity.Standing{VIP: true, SubTier: 2}.Badges())
	require.True(t, entity.Standing{VIP: true}.SkipsSlow())
	require.False(t, entity.Standing{VIP: true}.SkipsSubsOnly())
	require.True(t, entity.Standing{Admin: true}.Privileged())

	require.Equal(t, "u:1", entity.Principal{UserID: "1", AnonID: "a", ConnID: "c"}.ViewerKey())
	require.Equal(t, "a:a", entity.Principal{AnonID: "a", ConnID: "c"}.ViewerKey())
	require.Equal(t, "c:c", entity.Principal{ConnID: "c"}.ViewerKey())
	require.Equal(t, entity.ColorOf("x"), entity.ColorOf("x"))
}

func TestSettingsValidate(t *testing.T) {
	t.Parallel()

	require.NoError(t, entity.ChatSettings{SlowSec: 5, FollowersMin: new(10)}.Validate())

	err := entity.ChatSettings{SlowSec: 3, FollowersMin: new(11)}.Validate()
	require.ErrorIs(t, err, entity.ErrValidation)
	require.Equal(t, map[string]string{"slowSec": "ENUM", "followersMin": "ENUM"}, entity.DetailsOf(err))
}
