package led

import "time"

// helix turns two dots in opposite directions. They meet twice a revolution, and because dots add
// rather than overwrite, the meeting is a single brighter light for a moment before they separate
// again — which is the whole illusion: two things crossing read as one strand passing behind another.
func helix(p Palette) Frame {
	const revolution = 2400 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		phase := float64(elapsed%revolution) / float64(revolution) * Segments

		out := make([]Color, Segments)
		dot(out, phase, p.Nth(0), 1.6)
		dot(out, -phase, p.Nth(1), 1.6)
		return out
	}
}
