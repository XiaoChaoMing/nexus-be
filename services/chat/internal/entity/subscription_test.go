package entity_test

import (
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestParseSub(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_765_000_000, 0).UTC()

	tests := []struct {
		name       string
		in         string
		want       entity.SubGrant
		ok, active bool
	}{
		{name: "legacy tier", in: "2", want: entity.SubGrant{Tier: 2}, ok: true, active: true},
		{name: "with period", in: "1:1765000060", want: entity.SubGrant{Tier: 1, Until: now.Add(time.Minute)}, ok: true, active: true},
		{name: "period ended", in: "3:1765000000", want: entity.SubGrant{Tier: 3, Until: now}, ok: true},
		{name: "tier 4", in: "4:1765000060"},
		{name: "tier 0", in: "0"},
		{name: "empty", in: ""},
		{name: "not a number", in: "x:1"},
		{name: "bad until", in: "1:abc"},
		{name: "zero until", in: "1:0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := entity.ParseSub(tc.in)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.active, got.Active(now))

			if tc.active {
				require.Equal(t, tc.want.Tier, got.TierAt(now))
			} else {
				require.Zero(t, got.TierAt(now))
			}
		})
	}

	g := entity.SubGrant{Tier: 2, Until: now}
	require.Equal(t, "2:1765000000", g.Encode())

	back, ok := entity.ParseSub(g.Encode())
	require.True(t, ok)
	require.Equal(t, g, back)
}

func TestEmoteTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		st   entity.Standing
		want int
	}{
		{name: "viewer", st: entity.Standing{}, want: 0},
		{name: "sub 2", st: entity.Standing{SubTier: 2}, want: 2},
		{name: "streamer", st: entity.Standing{Streamer: true}, want: 3},
		{name: "moderator", st: entity.Standing{Moderator: true}, want: 1},
		{name: "vip with sub 3", st: entity.Standing{VIP: true, SubTier: 3}, want: 3},
		{name: "admin", st: entity.Standing{Admin: true}, want: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.st.EmoteTier())
		})
	}

	tier := 2
	locked := entity.Emote{Code: "kaelenHype", TierRequired: &tier}
	require.False(t, locked.UsableAt(1))
	require.True(t, locked.UsableAt(2))
	require.True(t, entity.Emote{Code: "nxGG"}.UsableAt(0))
}

func TestSubAnnouncement(t *testing.T) {
	t.Parallel()

	require.Equal(t, "@an vừa subscribe Tier 1", entity.NewSubAnnouncement(entity.SystemSubNew, "u1", "an", 1).Text)
	require.Equal(t, "Một người xem đã gia hạn Tier 2", entity.NewSubAnnouncement(entity.SystemSubRenew, "u1", "", 2).Text)
}
