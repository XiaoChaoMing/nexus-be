package entity_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestGuestKey(t *testing.T) {
	t.Parallel()

	const anon = "0199d4a2-0000-7000-8000-00000000a0a0"

	key := entity.GuestKey([]byte("secret"), anon)
	u, err := uuid.Parse(key)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(8), u.Version())
	require.Equal(t, uuid.RFC4122, u.Variant())

	require.Equal(t, key, entity.GuestKey([]byte("secret"), anon), "stable for one anonId")
	require.NotEqual(t, key, entity.GuestKey([]byte("other"), anon), "keyed by the secret")
	require.NotEqual(t, key, entity.GuestKey([]byte("secret"), "0199d4a2-0000-7000-8000-00000000a0a1"))
	require.NotEqual(t, anon, key)
}

func TestPrincipalAuthorID(t *testing.T) {
	t.Parallel()

	require.Equal(t, "u1", entity.Principal{UserID: "u1", GuestKey: "g1"}.AuthorID())
	require.Equal(t, "g1", entity.Principal{GuestKey: "g1"}.AuthorID())
	require.Empty(t, entity.Principal{}.AuthorID())
}

func TestGuestNameInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw    string
		want   string
		detail string
	}{
		{raw: "  Mèo   Con ", want: "Mèo Con"},
		{raw: "an_b-c.d 9", want: "an_b-c.d 9"},
		{raw: "Khách quen", want: "", detail: "TAKEN"},
		{raw: "Trần Văn A", want: "Trần Văn A"},
		{raw: "", detail: "REQUIRED"},
		{raw: " \t ", detail: "REQUIRED"},
		{raw: "ab", detail: "LENGTH"},
		{raw: strings.Repeat("a", 21), detail: "LENGTH"},
		{raw: strings.Repeat("a", 10000), detail: "LENGTH"},
		{raw: "a!b@c", detail: "FORMAT"},
		{raw: "___", detail: "FORMAT"},
		{raw: "mèo\u200bcon", detail: "FORMAT"},
		{raw: "xem abc.com", detail: "BLOCKED"},
		{raw: "Admin Minh", detail: "TAKEN"},
		{raw: "MOD", detail: "TAKEN"},
		{raw: "Hệ Thống", detail: "TAKEN"},
		{raw: "nexus live", detail: "TAKEN"},
		{raw: "Moderna", want: "Moderna"},
	}

	for _, tc := range tests {
		got, err := entity.GuestNameInput(tc.raw)
		if tc.detail == "" {
			require.NoError(t, err, tc.raw)
			require.Equal(t, tc.want, got)

			continue
		}

		require.ErrorIs(t, err, entity.ErrValidation, tc.raw)
		require.Equal(t, map[string]string{"guestName": tc.detail}, entity.DetailsOf(err), tc.raw)
	}
}

func TestGuestAuthor(t *testing.T) {
	t.Parallel()

	a := entity.GuestAuthor("g1", "Mèo Con")
	require.True(t, a.IsGuest())
	require.Empty(t, a.Username)
	require.Equal(t, "Mèo Con", a.DisplayName)
	require.Equal(t, entity.ColorOf("g1"), a.Color)
	require.False(t, entity.Author{Badges: []string{entity.BadgeStreamer}}.IsGuest())
}

func TestSameModesComparesGuestChat(t *testing.T) {
	t.Parallel()

	require.False(t, entity.ChatSettings{}.SameModes(entity.ChatSettings{GuestChat: true}))
	require.True(t, entity.ChatSettings{GuestChat: true}.Mode().GuestChat)
}
