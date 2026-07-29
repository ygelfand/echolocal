package led

import (
	"math"
	"time"
)

// orbits turns three dots at speeds that do not divide each other, so they pass and re-pass without
// ever settling into a pattern. Marbles knocking about a ring would look much the same: identical
// marbles that collide elastically swap velocities, which is indistinguishable from passing through
// each other, so there is nothing to simulate.
func orbits(p Palette) Frame {
	speeds := []float64{1, -0.62, 0.37}
	const revolution = 2600 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		t := float64(elapsed) / float64(revolution)

		out := make([]Color, Segments)
		for i, speed := range speeds {
			dot(out, math.Mod(t*speed, 1)*Segments, p.Nth(i), 1.4)
		}
		return out
	}
}
