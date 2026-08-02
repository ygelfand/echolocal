package led

import (
	"math"
	"time"
)

// scanner sweeps an eye round the ring and back again. The glow around the head is symmetric, unlike
// the comet's tail, so the turn at each end reads as the eye slowing and coming back rather than a
// tail flipping to the other side. The palette runs outwards from the head, so a hot core with cool
// edges is a matter of which colours it is given.
func scanner(p Palette) Frame {
	const (
		// One pass across the ring, so a there-and-back takes twice this.
		sweep = 900 * time.Millisecond

		// How far the glow reaches, in segments.
		width = 2.2
	)

	return func(elapsed time.Duration) []Color {
		// A triangle wave: across, then back.
		x := math.Mod(float64(elapsed)/float64(sweep), 2)
		if x > 1 {
			x = 2 - x
		}
		head := x * (Segments - 1)

		out := make([]Color, Segments)
		for i := range out {
			d := ringDist(i, head)
			if d >= width {
				continue
			}
			f := 1 - d/width
			out[i] = p.Shade(d/width, f*f)
		}
		return out
	}
}
