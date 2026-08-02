package led

import (
	"math"
	"time"
)

// ripple runs two crests round the ring over a low glow, slowly enough to read as water. Two,
// because a single crest travelling is a comet with soft edges; a second one opposite makes it read
// as a wave passing through the ring rather than something going round it.
//
// The palette is the depth of the water: a crest is drawn from the top of it and a trough from the
// bottom, so a gradient gives the wave something underneath it rather than only less light.
func ripple(p Palette) Frame {
	const (
		revolution = 3200 * time.Millisecond
		crests     = 2

		// What the troughs keep, so the ring stays present between crests instead of going out.
		glow = 0.12
	)

	return func(elapsed time.Duration) []Color {
		phase := float64(elapsed%revolution) / float64(revolution)

		out := make([]Color, Segments)
		for i := range out {
			// Cubed, which narrows the crest and widens the trough: an even swell around all twelve
			// segments looks like the ring breathing out of step with itself. Cubed rather than
			// squared because an even power is positive either side of the trough, and would put a
			// crest there too — twice as many as asked for.
			c := (1 + math.Cos(2*math.Pi*crests*(float64(i)/Segments-phase))) / 2
			c *= c * c
			out[i] = p.Shade(c, glow+(1-glow)*c)
		}
		return out
	}
}
