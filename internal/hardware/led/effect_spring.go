package led

import (
	"math"
	"time"
)

// spring stretches an arc out, then lets it go. What makes it a spring rather than an animation
// easing to a stop is that it passes where it is heading and comes back: each half overshoots and
// rings down around where it lands.
func spring(p Palette) Frame {
	const (
		// Out, then back. Each half is one damped step.
		half = 1300 * time.Millisecond

		// Overshoots per half, and how fast they die away. Ringing much longer than it takes to
		// settle is a wobble; dying much faster and the overshoot never shows at all.
		rings = 1.5
		decay = 3.0

		// Where each half is heading. Short of the ends on purpose: an overshoot past a full ring or
		// an empty one is clipped, and a spring that cannot overshoot is just a wipe.
		stretched = 0.72
		slack     = 0.12
	)

	// step is a damped approach from one to the other across a half, x running 0 to 1.
	step := func(from, to, x float64) float64 {
		return to + (from-to)*math.Exp(-decay*x)*math.Cos(2*math.Pi*rings*x)
	}

	return func(elapsed time.Duration) []Color {
		t := elapsed % (2 * half)

		var x float64
		if t < half {
			x = step(slack, stretched, float64(t)/float64(half))
		} else {
			x = step(stretched, slack, float64(t-half)/float64(half))
		}

		x = math.Max(0, math.Min(1, x))
		return Arc(x, p.Along(x))
	}
}
