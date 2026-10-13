package entity_test

import (
	"strings"
	"testing"

	"github.com/minhnn/nexuslive-be/services/channel/internal/entity"
	"github.com/stretchr/testify/require"
)

func TestBaseHandle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, username, want string
	}{
		{name: "lower case", username: "MinhNN", want: "minhnn"},
		{name: "keeps digits and underscore", username: "minh_nn_99", want: "minh_nn_99"},
		{name: "drops other characters", username: "minh.nn-Đức", want: "minhnnc"},
		{name: "pads short names", username: "ab", want: "ab_"},
		{name: "empty after filtering", username: "...", want: "channel"},
		{name: "cuts to 25", username: strings.Repeat("a", 30), want: strings.Repeat("a", 25)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := entity.BaseHandle(tc.username)
			require.Equal(t, tc.want, got)
			require.True(t, entity.ValidHandle(got))
		})
	}
}

func TestHandleCandidate(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 25)

	tests := []struct {
		name, base string
		n          int
		want       string
	}{
		{name: "first is the base", base: "minhnn", n: 1, want: "minhnn"},
		{name: "second gets 2", base: "minhnn", n: 2, want: "minhnn2"},
		{name: "third gets 3", base: "minhnn", n: 3, want: "minhnn3"},
		{name: "long base is cut for one digit", base: long, n: 2, want: strings.Repeat("a", 24) + "2"},
		{name: "long base is cut for two digits", base: long, n: 12, want: strings.Repeat("a", 23) + "12"},
		{name: "short base is not cut", base: "abc", n: 1000, want: "abc1000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := entity.HandleCandidate(tc.base, tc.n)
			require.Equal(t, tc.want, got)
			require.LessOrEqual(t, len(got), entity.HandleMaxLen)
			require.True(t, entity.ValidHandle(got))
		})
	}
}

func TestValidHandle(t *testing.T) {
	t.Parallel()

	require.True(t, entity.ValidHandle("MinhNN"))
	require.False(t, entity.ValidHandle("ab"))
	require.False(t, entity.ValidHandle(strings.Repeat("a", 26)))
	require.False(t, entity.ValidHandle("minh-nn"))
	require.False(t, entity.ValidHandle("minh%20nn"))
}

func TestDisplayName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Minh", entity.DisplayName("  Minh ", "minhnn"))
	require.Equal(t, "minhnn", entity.DisplayName("   ", "minhnn"))
	require.Equal(t, strings.Repeat("ă", 50), entity.DisplayName(strings.Repeat("ă", 60), "x"))
}
