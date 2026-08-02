package led

import (
	"math"
	"time"
)

// twinkle brightens segments one at a time on their own schedules, which is what makes it look
// random while keeping nothing between frames: each segment takes a period, a phase and a place in
// the palette from its index, and the periods do not divide each other, so the ring takes a long
// time to repeat itself.
func twinkle(p Palette) Frame {
	const (
		shortest = 1700 * time.Millisecond
		spread   = 1300 * time.Millisecond

		// How much of its own cycle a segment spends lit, and what it keeps the rest of the time.
		duty = 0.35
		dark = 0.05
	)

	sparks := make([]struct {
		period time.Duration
		phase  float64
		color  Color
	}, Segments)

	for i := range sparks {
		// Hashed rather than drawn from a source of randomness, so the ring twinkles the same way
		// after a restart and a test can say what it expects. The offset keeps segment 0 from
		// hashing to zero and starting every cycle exactly on time.
		h := hash(uint32(i) + 0x9E3779B9)
		sparks[i].period = shortest + time.Duration(float64(spread)*float64(h&0xFFFF)/0x10000)
		sparks[i].phase = float64(h>>16) / 0x10000
		sparks[i].color = p.At(sparks[i].phase)
	}

	return func(elapsed time.Duration) []Color {
		out := make([]Color, Segments)
		for i, s := range sparks {
			f := dark
			if x := math.Mod(float64(elapsed)/float64(s.period)+s.phase, 1); x < duty {
				f = math.Max(dark, math.Sin(math.Pi*x/duty))
			}
			out[i] = scale(s.color, f)
		}
		return out
	}
}
