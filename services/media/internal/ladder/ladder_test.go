package ladder_test

import (
	"testing"

	"github.com/minhnn/nexuslive-be/services/media/internal/ladder"
	"github.com/stretchr/testify/assert"
)

func names(h int) []string {
	out := []string{}
	for _, r := range ladder.For(h) {
		out = append(out, r.Name)
	}

	return out
}

func TestFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		height int
		want   []string
	}{
		{name: "1080p source keeps full ladder", height: 1080, want: []string{"720p", "480p", "360p"}},
		{name: "720p source", height: 720, want: []string{"720p", "480p", "360p"}},
		{name: "540p source drops 720p", height: 540, want: []string{"480p", "360p"}},
		{name: "480p source", height: 480, want: []string{"480p", "360p"}},
		{name: "unknown source assumes 1080p", height: 0, want: []string{"720p", "480p", "360p"}},
		{name: "tiny source keeps one rendition", height: 241, want: []string{"360p"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, names(tc.height))
		})
	}

	assert.Equal(t, 240, ladder.For(241)[0].Height, "never upscales, height stays even")
}
