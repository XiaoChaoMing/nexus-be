package entity_test

import (
	"strings"
	"testing"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/stretchr/testify/require"
)

const (
	_secret = "0123456789abcdef0123456789abcdef"
	_userID = "0199d4a2-0000-7000-8000-000000000002"
)

func TestUnsubscribeTokenRoundTrip(t *testing.T) {
	t.Parallel()

	for _, typ := range entity.Types() {
		tok, ok := entity.UnsubscribeToken(_secret, _userID, typ)
		require.True(t, ok)

		user, got, err := entity.ParseUnsubscribeToken(_secret, tok)
		require.NoError(t, err)
		require.Equal(t, _userID, user)
		require.Equal(t, typ, got)
	}
}

func TestUnsubscribeTokenRejects(t *testing.T) {
	t.Parallel()

	tok, ok := entity.UnsubscribeToken(_secret, _userID, entity.TypeCreatorLive)
	require.True(t, ok)

	body, mac, _ := strings.Cut(tok, ".")
	flip := func(s string, i int) string {
		c := byte('A')
		if s[i] == 'A' {
			c = 'B'
		}

		return s[:i] + string(c) + s[i+1:]
	}
	other, _ := entity.UnsubscribeToken(_secret, _userID, entity.TypeMention)
	otherBody, _, _ := strings.Cut(other, ".")

	tests := map[string]string{
		"empty":                "",
		"no separator":         body + mac,
		"tampered body (user)": flip(body, 2) + "." + mac,
		"tampered mac":         body + "." + flip(mac, 3),
		"type swapped":         otherBody + "." + mac,
		"not base64":           "!!!." + mac,
		"short body":           body[:10] + "." + mac,
		"unknown type byte":    "AZnUogAAcACAAAAAAAAAAv8." + mac,
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := entity.ParseUnsubscribeToken(_secret, in)
			require.ErrorIs(t, err, entity.ErrTokenInvalid)
		})
	}

	t.Run("another secret voids every token", func(t *testing.T) {
		t.Parallel()

		_, _, err := entity.ParseUnsubscribeToken(strings.Repeat("x", 32), tok)
		require.ErrorIs(t, err, entity.ErrTokenInvalid)
	})
}

func TestEffectiveKeepsLocks(t *testing.T) {
	t.Parallel()

	s := entity.Effective(entity.TypeSystem, &entity.Setting{Type: entity.TypeSystem})
	require.True(t, s.InApp)
	require.True(t, s.Email)

	s = entity.Effective(entity.TypeNewFollower, &entity.Setting{Type: entity.TypeNewFollower, InApp: true, Email: true})
	require.False(t, s.Email)
	require.True(t, entity.Setting{Type: entity.TypeMention, InApp: true}.IsDefault())
}
