package app

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/stretchr/testify/require"
)

const _testChannel = "0199d4a2-0000-7000-8000-0000000000cc"

func TestParseEmote(t *testing.T) {
	t.Parallel()

	ok := []string{"--channel", _testChannel, "--tier", "2", "--code", "kaelenHype", "--image", "/emotes/channel/kaelenHype.webp"}

	tests := []struct {
		name    string
		cmd     string
		args    []string
		wantErr error
	}{
		{name: "add", cmd: "add", args: ok},
		{name: "add media", cmd: "add", args: []string{"--channel", _testChannel, "--tier", "1", "--code", "ab", "--image", "/media/e/x.webp"}},
		{name: "bad channel", cmd: "add", args: []string{"--channel", "x"}, wantErr: errEmoteChannel},
		{name: "tier 4", cmd: "add", args: []string{"--channel", _testChannel, "--tier", "4", "--code", "ab", "--image", "/emotes/channel/a.webp"}, wantErr: errBadTier},
		{name: "bad code", cmd: "add", args: []string{"--channel", _testChannel, "--tier", "1", "--code", "a-b", "--image", "/emotes/channel/a.webp"}, wantErr: errEmoteCode},
		{name: "external image", cmd: "add", args: []string{"--channel", _testChannel, "--tier", "1", "--code", "ab", "--image", "https://x/a.webp"}, wantErr: errEmoteImage},
		{name: "path escape", cmd: "add", args: []string{"--channel", _testChannel, "--tier", "1", "--code", "ab", "--image", "/emotes/channel/../a.webp"}, wantErr: errEmoteImage},
		{name: "remove", cmd: "remove", args: []string{"--channel", _testChannel, "--code", "kaelenHype"}},
		{name: "remove bad code", cmd: "remove", args: []string{"--channel", _testChannel, "--code", "x"}, wantErr: errEmoteCode},
		{name: "seed", cmd: "seed-demo", args: []string{"--channel", _testChannel}},
		{name: "unknown", cmd: "list", args: []string{"--channel", _testChannel}, wantErr: errEmoteUsage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseEmote(tc.cmd, tc.args)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

type recEmotes struct {
	repo.EmoteRepo
	added []entity.Emote
}

func (r *recEmotes) AddChannelEmote(_ context.Context, e entity.Emote) error {
	r.added = append(r.added, e)

	return nil
}

func TestSeedDemo(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	r := &recEmotes{}

	require.NoError(t, runEmote(context.Background(), r, "seed-demo", emoteArgs{channel: _testChannel}, &out))
	require.Len(t, r.added, 5)

	for _, e := range r.added {
		require.Equal(t, _testChannel, *e.ChannelID)
		require.Equal(t, "/emotes/channel/"+e.Code+".webp", e.ImageURL)
		require.NoError(t, validateEmote(emoteArgs{channel: _testChannel, code: e.Code, image: e.ImageURL, tier: *e.TierRequired}))
	}
}

func TestSubUntil(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	require.Equal(t, now.Add(entity.SubPeriod), subUntil(now, 0))
	require.Equal(t, now.Add(2*time.Minute), subUntil(now, 2))

	_, err := parseGrant([]string{"sub", "--channel", _testChannel, "--user", _testChannel, "--minutes", "-1"})
	require.ErrorIs(t, err, errBadMins)
}
