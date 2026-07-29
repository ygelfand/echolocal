package led

import (
	"math"
	"time"
)

// breathe pulses the whole ring.
func breathe(p Palette) Frame {
	const period = 1200 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		f := (1 - math.Cos(2*math.Pi*float64(elapsed%period)/float64(period))) / 2
		return around(p, f)
	}
}
