package led

import "time"

// wipe fills the ring from the front and empties it again. It is the shape of something being
// counted out rather than something travelling, which is why it fills all the way before it turns
// round: a wipe that reverses part way looks like it changed its mind.
func wipe(p Palette) Frame {
	const period = 2400 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		x := 2 * float64(elapsed%period) / float64(period)
		if x > 1 {
			x = 2 - x
		}
		return Arc(x, p.Along(x))
	}
}
