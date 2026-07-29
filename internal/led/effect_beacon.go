package led

import "time"

// beacon sweeps a bright arc round the ring, the way a warning light on a vehicle does: not urgent
// the way the pulses are, but impossible to mistake for the ring simply being on.
func beacon(p Palette) Frame {
	const (
		revolution = 900 * time.Millisecond
		width      = 2.5
	)

	return func(elapsed time.Duration) []Color {
		at := float64(elapsed%revolution) / float64(revolution) * Segments

		out := make([]Color, Segments)
		for i := range out {
			d := ringDist(i, at)
			if d >= width {
				continue
			}
			f := 1 - d/width
			out[i] = p.Shade(f, f*f)
		}
		return out
	}
}
