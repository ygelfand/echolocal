package led

import (
	"math"
	"time"
)

// pinwheel turns four arms, ninety degrees apart. Four, because twelve divides by it: every arm
// lands on a segment at the same moment, so the wheel looks rigid rather than flexing as it turns.
// Each arm carries its own colour from the palette, and carries it round with it.
func pinwheel(p Palette) Frame {
	const (
		revolution = 1600 * time.Millisecond
		arms       = 4

		// Segments between one arm and the next.
		span = Segments / arms
	)

	return func(elapsed time.Duration) []Color {
		at := float64(elapsed%revolution) / float64(revolution) * Segments

		out := make([]Color, Segments)
		for i := range out {
			// Which arm is nearest, and how far off it this segment is. The arms repeat every span
			// segments, so the whole wheel is one distance measured within a single span — and the arm
			// number comes from the same division, which is what keeps a colour with its arm.
			turns := (float64(i) - at) / span
			arm := int(math.Round(turns))

			// Split linearly between the two segments an arm falls between, rather than by a curve.
			// A curve looks sharper standing still and pulses as it turns: what the two segments lose
			// between them is what the arm dims by every time it passes a gap.
			if d := math.Abs(turns-float64(arm)) * span; d < 1 {
				out[i] = scale(p.Nth(arm), 1-d)
			}
		}
		return out
	}
}
