package led

import "time"

// Nothing in a room effect rests lit. A dark ring does read as broken rather than as calm, which is why
// all of these kept a little light at the bottom to begin with — but on this hardware a lit segment
// whines audibly across the room, so an effect meant to be left on all evening has to be silent when
// nothing is happening, and the only silent light is none.
//
// That puts the weight on the level actually reaching zero, which against a floor that follows the
// quietest recent moment it may well not: see internal/mic/level.go.

// roomGlow brightens the whole ring with the room, and goes out with it.
func roomGlow(p Palette, r Room) Frame {
	return func(time.Duration) []Color {
		return around(p, level(r))
	}
}
