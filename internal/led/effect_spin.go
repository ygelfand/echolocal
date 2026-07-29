package led

import "time"

// spin turns the palette round the ring. It has no motion of its own beyond that, so it is only
// worth running with colours that have somewhere to go — a single colour spun looks like a ring that
// is simply on.
func spin(p Palette) Frame {
	const revolution = 1500 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		phase := float64(elapsed%revolution) / float64(revolution)
		out := make([]Color, Segments)
		for i := range out {
			out[i] = p.At(float64(i)/Segments + phase)
		}
		return out
	}
}
