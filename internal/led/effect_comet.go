package led

import "time"

// comet runs a bright head clockwise with a decaying tail behind it. The palette runs along the tail
// as well as down it: one colour simply fades, where a flame's colours cool from the head backwards
// the way a spark thrown off a fire does.
func comet(p Palette) Frame {
	// Two frames per segment, so the head advances evenly.
	const perSegment = 2 * FrameInterval
	tail := []float64{1, 0.55, 0.3, 0.16, 0.08}

	return func(elapsed time.Duration) []Color {
		head := int(elapsed/perSegment) % Segments
		out := make([]Color, Segments)
		for i, f := range tail {
			c := p.Shade(float64(i)/float64(len(tail)-1), f)
			out[((head-i)%Segments+Segments)%Segments] = c
		}
		return out
	}
}
