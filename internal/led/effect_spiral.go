package led

import (
	"math"
	"time"
)

// spiral runs a brightness gradient round the ring and turns it. Where the comet is a head with a
// short tail, this is lit the whole way round and only says which way it is going, which makes it the
// calmest way the ring can show that something is turning.
func spiral(p Palette) Frame {
	const revolution = 2200 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		phase := float64(elapsed%revolution) / float64(revolution)

		out := make([]Color, Segments)
		for i := range out {
			// How far behind the leading edge this segment is, as a fraction of the way round.
			behind := math.Mod(float64(i)/Segments-phase+1, 1)
			f := 1 - behind
			out[i] = p.Shade(f, f)
		}
		return out
	}
}
