package led

import (
	"math"
	"time"
)

// flicker is a flame. The ring as a whole wanders in brightness while each segment wanders a little
// around it, so it moves as one thing rather than as twelve separate candles.
//
// With one colour that is a candle. With a palette from the base of a flame outwards it is a fire:
// the segment's own wander decides how deep into the flame it is at that moment, so the hot colours
// appear where it happens to be brightest, which is the difference between a fire and an orange ring
// going up and down.
func flicker(p Palette) Frame {
	return func(elapsed time.Duration) []Color {
		t := elapsed.Seconds()
		whole := 0.75 + 0.25*wander(t, 0)

		out := make([]Color, Segments)
		for i := range out {
			w := wander(t, float64(i)*0.7)
			f := whole * (0.8 + 0.2*w)

			// Along from the far end, because a flame palette runs hottest first: the brighter this
			// segment is at this moment, the further into the base of the flame it is. Sampled the other
			// way round it is still a fire of sorts, but the hot colours land on the dim segments, which
			// is the one thing a fire never does.
			out[i] = scale(p.Along(1-w), math.Min(1, f))
		}
		return out
	}
}
