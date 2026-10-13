// Package ladder chooses the ABR renditions for a source.
package ladder

import "github.com/minhnn/nexuslive-be/services/media/internal/entity"

// DefaultSourceHeight is assumed when the encoder does not announce its resolution.
const DefaultSourceHeight = 1080

// Full is the complete ladder, highest first (TSD §5.3).
//
//nolint:mnd // the ladder table is data
func Full() []entity.Rendition {
	return []entity.Rendition{
		{Name: "720p", Height: 720, VideoKbps: 2500, MaxrateKbps: 2675, BufsizeKbps: 3750, AudioKbps: 128},
		{Name: "480p", Height: 480, VideoKbps: 1200, MaxrateKbps: 1284, BufsizeKbps: 1800, AudioKbps: 128},
		{Name: "360p", Height: 360, VideoKbps: 700, MaxrateKbps: 749, BufsizeKbps: 1050, AudioKbps: 96},
	}
}

// For returns the renditions not taller than the source; the lowest one is always kept so every
// stream has at least one output, even below 360p.
func For(sourceHeight int) []entity.Rendition {
	if sourceHeight <= 0 {
		sourceHeight = DefaultSourceHeight
	}

	full := Full()
	out := make([]entity.Rendition, 0, len(full))

	for _, r := range full {
		if r.Height <= sourceHeight {
			out = append(out, r)
		}
	}

	if len(out) == 0 {
		lowest := full[len(full)-1]
		lowest.Height = sourceHeight - sourceHeight%2 //nolint:mnd // H.264 needs an even height
		out = append(out, lowest)
	}

	return out
}
