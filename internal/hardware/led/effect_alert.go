package led

import (
	"math"
	"time"
)

// alert pulses three times and rests. Three, because one is a glitch and two is a heartbeat; the rest
// after them is what makes the group read as a thing being said rather than as a rhythm.
//
// Each pulse swells rather than snapping on. On twelve segments a hard edge reads as the driver
// faulting, which is the one impression a failure indication cannot afford.
func alert(p Palette) Frame {
	const (
		period = 2 * time.Second
		pulse  = 130 * time.Millisecond
		gap    = 90 * time.Millisecond
		pulses = 3
	)

	return func(elapsed time.Duration) []Color {
		t := elapsed % period

		var f float64
		for i := range pulses {
			at := time.Duration(i) * (pulse + gap)
			if t >= at && t < at+pulse {
				f = math.Sin(math.Pi * float64(t-at) / float64(pulse))
			}
		}
		return shaded(p, f)
	}
}
