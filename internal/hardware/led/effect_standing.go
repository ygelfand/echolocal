package led

import (
	"math"
	"time"
)

// standing adds two waves going opposite ways round the ring. Where they meet in step the ring is
// brightest, where they cancel it is dark, and the difference from ripple is that the dark points
// stay where they are: the pattern breathes in place instead of travelling. Two crests each way put
// the still points on segments rather than between them, which twelve allows and most counts do not.
func standing(p Palette) Frame {
	const (
		period = 2600 * time.Millisecond
		crests = 2
		glow   = 0.08
	)

	return func(elapsed time.Duration) []Color {
		swell := math.Cos(2 * math.Pi * float64(elapsed%period) / float64(period))

		out := make([]Color, Segments)
		for i := range out {
			f := math.Abs(math.Sin(2*math.Pi*crests*float64(i)/Segments) * swell)
			out[i] = p.Shade(f, glow+(1-glow)*f)
		}
		return out
	}
}
