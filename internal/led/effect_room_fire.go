package led

import (
	"math"
	"time"
)

// roomFire is a fire the room feeds: it goes out in silence, catches at a word and burns while people
// are talking. The flicker underneath means it is never a still picture of a fire while it is lit.
//
// The room sets how far up the flame the ring may reach and the flicker moves underneath that, which
// is what keeps the two readable at once: a little noise is dim and dark red, a room full of talking is
// bright and nearly white, and the difference is a colour rather than only a brightness.
func roomFire(p Palette, l Level) Frame {
	const (
		// What a silent room leaves burning. Embers have to be visible, or a quiet room looks like a
		// fault — and the far end of a flame palette is nearly black on purpose, because there it sits
		// next to the bright parts. Alone it needs both a floor under the brightness and one under how
		// far down the palette it may go.
		resting = 0.3
		coolest = 0.1
	)

	return func(elapsed time.Duration) []Color {
		x := level(l)
		t := elapsed.Seconds()
		whole := 0.75 + 0.25*wander(t, 0)

		out := make([]Color, Segments)
		for i := range out {
			w := wander(t, float64(i)*0.7)

			// Sampled from the far end, because a flame palette runs hottest first.
			heat := (coolest + (1-coolest)*x) * (0.55 + 0.45*w)
			f := x * whole * (0.8 + 0.2*w)
			out[i] = scale(p.Along(1-heat), math.Min(1, f))
		}
		return out
	}
}
