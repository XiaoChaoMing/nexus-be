package entity_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/stretchr/testify/require"
)

var errDB = errors.New("db down")

func TestCheckNewHandle(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"minh_nn99":             "",
		"abc":                   "",
		"ab":                    entity.DetailFormat,
		"MinhNN":                entity.DetailFormat,
		"minh-nn":               entity.DetailFormat,
		"minh nn":               entity.DetailFormat,
		strings.Repeat("a", 26): entity.DetailFormat,
		"studio":                entity.DetailInvalid,
		"browse":                entity.DetailInvalid,
		"nexuslive":             entity.DetailInvalid,
	}

	for h, want := range tests {
		require.Equal(t, want, entity.CheckNewHandle(h), h)
	}
}

func ptr[T any](v T) *T { return &v }

func TestProfilePatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		patch entity.ProfilePatch
		want  map[string]string
	}{
		{name: "empty", patch: entity.ProfilePatch{}, want: map[string]string{"body": entity.DetailRequired}},
		{name: "blank name", patch: entity.ProfilePatch{DisplayName: ptr("   ")}, want: map[string]string{"displayName": entity.DetailLength}},
		{name: "bio 300", patch: entity.ProfilePatch{Bio: ptr(strings.Repeat("ữ", 300))}, want: map[string]string{}},
		{name: "bio 301", patch: entity.ProfilePatch{Bio: ptr(strings.Repeat("ữ", 301))}, want: map[string]string{"bio": entity.DetailLength}},
		{
			name: "six links", patch: entity.ProfilePatch{SocialLinks: ptr(make([]entity.SocialLink, 6))},
			want: map[string]string{"socialLinks": entity.DetailMax},
		},
		{
			name: "http link", patch: entity.ProfilePatch{SocialLinks: &[]entity.SocialLink{{Type: "youtube", URL: "http://youtube.com/x"}}},
			want: map[string]string{"socialLinks": entity.DetailFormat},
		},
		{
			name: "javascript link", patch: entity.ProfilePatch{SocialLinks: &[]entity.SocialLink{{Type: "X", URL: "javascript:alert(1)"}}},
			want: map[string]string{"socialLinks": entity.DetailFormat},
		},
		{
			name: "unknown kind", patch: entity.ProfilePatch{SocialLinks: &[]entity.SocialLink{{Type: "MYSPACE", URL: "https://a.b"}}},
			want: map[string]string{"socialLinks": entity.DetailEnum},
		},
		{
			name: "links ok", patch: entity.ProfilePatch{SocialLinks: &[]entity.SocialLink{{Type: " discord ", URL: " https://discord.gg/x "}}},
			want: map[string]string{},
		},
		{name: "reserved handle", patch: entity.ProfilePatch{Handle: ptr("admin")}, want: map[string]string{"handle": entity.DetailInvalid}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.patch.Normalize().Validate())
		})
	}

	p := entity.ProfilePatch{Bio: ptr("  ")}.Normalize()
	require.True(t, p.ClearBio, "a blank bio clears it")
	require.Nil(t, p.Bio)
}

func TestHandleChange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	month := 30 * 24 * time.Hour
	changed := now.Add(-10 * 24 * time.Hour)
	ch := entity.Channel{Handle: "minhnn", HandleChangedAt: &changed}

	changes, next := ch.HandleChange("MinhNN", month, now)
	require.False(t, changes, "same handle in another case")
	require.Nil(t, next)

	changes, next = ch.HandleChange("other", month, now)
	require.True(t, changes)
	require.Equal(t, changed.Add(month), *next, "second change inside 30 days")
	require.Equal(t, changed.Add(month), *ch.NextHandleChangeAt(month, now))

	changes, next = ch.HandleChange("other", month, now.Add(21*24*time.Hour))
	require.True(t, changes)
	require.Nil(t, next)
	require.Nil(t, entity.Channel{}.NextHandleChangeAt(month, now), "never changed")
}

func TestNormalizeQuery(t *testing.T) {
	t.Parallel()

	q, ok := entity.NormalizeQuery("  Liên  ")
	require.True(t, ok)
	require.Equal(t, "Liên", q, "NFD input becomes NFC")

	_, ok = entity.NormalizeQuery(" a ")
	require.False(t, ok)

	_, ok = entity.NormalizeQuery(strings.Repeat("ư", 101))
	require.False(t, ok)

	_, ok = entity.NormalizeQuery(strings.Repeat("ư", 100))
	require.True(t, ok)
}

func TestCodeOf(t *testing.T) {
	t.Parallel()

	require.Equal(t, entity.CodeConflict, entity.CodeOf(entity.Reject(entity.ErrHandleTooSoon, "nextChangeAt", "x")))
	require.Equal(t, map[string]string{"nextChangeAt": "x"}, entity.DetailsOf(entity.Reject(entity.ErrHandleTooSoon, "nextChangeAt", "x")))
	require.Equal(t, entity.CodeValidationFailed, entity.CodeOf(entity.ErrImageFormat))
	require.Equal(t, entity.CodePayloadTooLarge, entity.CodeOf(entity.ErrImageTooLarge))
	require.Equal(t, entity.CodeInternal, entity.CodeOf(errDB))
	require.Nil(t, entity.DetailsOf(errDB))
}
