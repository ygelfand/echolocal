package led

import (
	"math"
	"time"
)

// heartbeat thumps twice and rests, the way a pulse is felt rather than the way it looks on a
// monitor: a strong beat, a weaker one just behind it, then a gap long enough that the pair reads as
// one heartbeat instead of a fast blink.
//
// How hard the beat is takes the palette as well as the brightness, rather than spreading it round
// the ring: a heartbeat has no shape across the ring to spread anything over, and what it does have
// is force. So the strong beat climbs to the top of the palette and the weaker one behind it does not
// reach as far, which is the difference a colour can show and brightness alone cannot.
func heartbeat(p Palette) Frame {
	const period = 1400 * time.Millisecond

	// How quickly a thump dies away. Short, so the ring is dark for most of the period, which is
	// where the resting comes from.
	const decay = 170 * time.Millisecond

	beats := []struct {
		at time.Duration
		f  float64
	}{
		{0, 1},
		{200 * time.Millisecond, 0.6},
	}

	return func(elapsed time.Duration) []Color {
		t := elapsed % period

		var f float64
		for _, b := range beats {
			if t < b.at {
				continue
			}
			f = math.Max(f, b.f*math.Exp(-float64(t-b.at)/float64(decay)))
		}

		return shaded(p, f)
	}
}
