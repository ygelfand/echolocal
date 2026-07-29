package led

import "time"

// chase lights every third segment and steps the pattern round, the theatre marquee. Every third,
// because twelve divides by three and the gaps stay even across the wrap: a spacing the ring does
// not divide by leaves one short gap, and the pattern looks broken at that one place.
//
// Each lamp keeps its own colour from the palette, so a wheel is a marquee of four different lamps
// rather than four of the same.
func chase(p Palette) Frame {
	const (
		step    = 110 * time.Millisecond
		spacing = 3
	)

	return func(elapsed time.Duration) []Color {
		out := make([]Color, Segments)
		off := int(elapsed/step) % spacing
		for lamp := range Segments / spacing {
			out[off+lamp*spacing] = p.Nth(lamp)
		}
		return out
	}
}
