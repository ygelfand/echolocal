package led

import (
	"math"
	"time"
)

// drift moves the palette round the ring unevenly and slowly, the way an aurora does: where a
// segment is looking in the palette is decided by two slow waves of different rates, so bands form,
// stretch and fold instead of marching past. A third decides brightness, so they fade in and out
// where they are rather than only sliding.
func drift(p Palette) Frame {
	return func(elapsed time.Duration) []Color {
		t := elapsed.Seconds()

		out := make([]Color, Segments)
		for i := range out {
			pos := float64(i) / Segments
			x := pos + 0.03*t +
				0.10*math.Sin(2*math.Pi*(0.07*t+pos)) +
				0.06*math.Sin(2*math.Pi*(0.13*t+2*pos))
			f := 0.45 + 0.55*(0.5+0.5*math.Sin(2*math.Pi*(0.11*t+1.5*pos)))
			out[i] = scale(p.At(x), f)
		}
		return out
	}
}
