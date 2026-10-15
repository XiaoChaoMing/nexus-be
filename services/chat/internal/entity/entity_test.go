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

	emotes := map[string]entity.Emote{"nxGG": {ID: "e1", Code: "nxGG", ImageURL: "/emotes/nxGG.webp"}}
	users := map[string]string{"minhnn": "u1"}

	got := entity.BuildFragments("gg nxGG @MinhNN, hi @nobody nxlgg", emotes, users)
	require.Equal(t, []entity.Fragment{
		{Type: "text", Text: "gg "},
		{Type: "emote", Text: "nxGG", Emote: &entity.EmoteRef{ID: "e1", Code: "nxGG", ImageURL: "/emotes/nxGG.webp"}},
		{Type: "text", Text: " "},
		{Type: "mention", Text: "@MinhNN", UserID: "u1"},
		{Type: "text", Text: ", hi @nobody nxlgg"},
	}, got)
	require.Equal(t, []string{"minhnn", "nobody"}, entity.MentionCandidates("@MinhNN, @nobody @minhnn x@y"))
}

func TestParseTopic(t *testing.T) {
	t.Parallel()

	id := "0199d4a2-0000-7000-8000-000000000001"

	for _, ok := range []string{"chat:" + id, "stream:" + id, "user:" + id, "sidebar:" + id, "chat:" + strings.ToUpper(id)} {
		tp, err := entity.ParseTopic(ok)
		require.NoError(t, err, ok)
		require.Equal(t, id, tp.ID)
	}

	for _, bad := range []string{"", "chat", "chat:x", "home:" + id, "sidebar:x", "chat:" + id + "x", "chat:{" + id + "}"} {
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

func TestNormalizeAndMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msg, term string
		want      bool
	}{
		{msg: "SPẢM!!", term: "spam", want: true},
		{msg: "đây là spam nè", term: "spam", want: true},
		{msg: "spammer", term: "spam", want: false},
		{msg: "Lua dao day", term: entity.Normalize("lừa đảo"), want: true},
		{msg: "LỪA   ĐẢO", term: "lua dao", want: true},
		{msg: "lua", term: "lua dao", want: false},
		{msg: "Việt Nam vô địch", term: entity.Normalize("vô địch"), want: true},
		{msg: "nxGG spam", term: "nxgg", want: true},
	}

	for _, tc := range tests {
		require.Equal(t, tc.want, entity.MatchTerm(entity.Normalize(tc.msg), tc.term), tc.msg+" / "+tc.term)
	}

	// NFD input normalizes like NFC.
	require.Equal(t, entity.Normalize("Việt"), entity.Normalize("Việt"))
	require.Equal(t, "d", entity.Normalize("Đ"))
}

func TestHasLink(t *testing.T) {
	t.Parallel()

	for _, yes := range []string{
		"xem tại abc.xyz", "https://example.org/x", "http://a.b", "www.something", "shop.abc.com.vn", "abc.vn/path", "ABC.COM",
		"link:abc.gg",
	} {
		require.True(t, entity.HasLink(yes), yes)
	}

	for _, no := range []string{"3.14 điểm", "a.b", "mail a@b.com nhé", "hello world", "v1.2.3", "file.txt"} {
		require.False(t, entity.HasLink(no), no)
	}
}

func TestAutoModRules(t *testing.T) {
	t.Parallel()

	rules := entity.AutoModRules{Terms: []string{"spam"}, BlockLinks: true}

	err := rules.Check("SPẢM nè", entity.Standing{})
	require.ErrorIs(t, err, entity.ErrContentBlocked)
	require.Equal(t, map[string]string{"reason": "TERM"}, entity.DetailsOf(err))

	err = rules.Check("xem tại abc.xyz", entity.Standing{})
	require.Equal(t, map[string]string{"reason": "LINK"}, entity.DetailsOf(err))

	require.NoError(t, rules.Check("xem tại abc.xyz", entity.Standing{VIP: true}), "VIPs may post links")
	require.ErrorIs(t, rules.Check("spam", entity.Standing{VIP: true}), entity.ErrContentBlocked, "but not blocked terms")
	require.NoError(t, rules.Check("spam abc.xyz", entity.Standing{Moderator: true}))
	require.NoError(t, rules.Check("spam abc.xyz", entity.Standing{Streamer: true}))
	require.NoError(t, rules.Check("spam abc.xyz", entity.Standing{Admin: true}))
	require.NoError(t, entity.AutoModRules{Terms: []string{"spam"}}.Check("abc.xyz", entity.Standing{}), "links allowed")
}

func TestTermInput(t *testing.T) {
	t.Parallel()

	display, term, err := entity.TermInput("  Lừa   Đảo ")
	require.NoError(t, err)
	require.Equal(t, "Lừa Đảo", display)
	require.Equal(t, "lua dao", term)

	_, _, err = entity.TermInput("a")
	require.Equal(t, map[string]string{"term": entity.DetailLength}, entity.DetailsOf(err))

	_, _, err = entity.TermInput("   ")
	require.Equal(t, map[string]string{"term": entity.DetailRequired}, entity.DetailsOf(err))

	_, _, err = entity.TermInput("!!")
	require.Equal(t, map[string]string{"term": entity.DetailLength}, entity.DetailsOf(err), "nothing left after normalization")

	_, _, err = entity.TermInput(strings.Repeat("a", 101))
	require.ErrorIs(t, err, entity.ErrValidation)
}

func TestMentionedAndExcerpt(t *testing.T) {
	t.Parallel()

	frags := []entity.Fragment{{Type: "mention", UserID: "me"}}
	for _, id := range []string{"a", "b", "a", "c", "d", "e", "f"} {
		frags = append(frags, entity.Fragment{Type: "mention", UserID: id})
	}

	m := entity.Message{User: entity.Author{ID: "me"}, Fragments: frags, Body: strings.Repeat("x ", 80)}
	require.Equal(t, []string{"a", "b", "c", "d", "e"}, m.Mentioned(), "sender left out, 5 at most, in order")
	require.Len(t, []rune(m.Excerpt()), entity.MentionExcerptMax)

	reason := strings.Repeat("r", 501)
	_, err := entity.CleanReason(&reason)
	require.ErrorIs(t, err, entity.ErrValidation)

	empty := "   "
	got, err := entity.CleanReason(&empty)
	require.NoError(t, err)
	require.Nil(t, got)
}
