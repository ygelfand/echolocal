package led

import (
	"math"
	"time"
)

// embers is flicker with the occasional spark thrown off it. A fire does not only vary — every so
// often a piece of it flares brighter than the flame ever gets, and that is most of what tells a fire
// from an orange light being turned up and down.
//
// Sparks are rare enough that two rarely overlap, so each segment carries its own schedule from its
// hash, the same trick twinkle uses. What a spark does is reach past the top of the palette: it is
// the one thing here allowed to be brighter than the flame.
func embers(p Palette) Frame {
	base := flicker(p)

	const (
		// How often a given segment sparks, and how long one lasts. Twelve segments on this schedule
		// is a spark every second or so somewhere on the ring.
		every = 9 * time.Second
		lasts = 260 * time.Millisecond
	)

	phases := make([]float64, Segments)
	for i := range phases {
		phases[i] = float64(hash(uint32(i)+0x51ED2701)) / float64(1<<32)
	}

	return func(elapsed time.Duration) []Color {
		out := base(elapsed)
		for i, phase := range phases {
			x := math.Mod(float64(elapsed)/float64(every)+phase, 1)
			if x >= float64(lasts)/float64(every) {
				continue
			}

			// Up fast and down slower, which is what a spark does.
			f := x / (float64(lasts) / float64(every))
			f = math.Sin(math.Pi * math.Pow(f, 0.7))
			out[i] = add(out[i], scale(p.Along(0), 0.7*f))
		}
		return out
	}
}
