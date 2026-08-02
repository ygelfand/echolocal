package led

import (
	"math"
	"time"
)

// follower points at whoever is talking. The array can tell roughly where a sound came from, so the ring
// turns towards it and widens as the room gets louder — the one thing twelve lights on a seven microphone
// array can do that no strip of LEDs can.
//
// Where it is pointing is carried between frames and eased towards the answer rather than jumped to it.
// Direction arrives in sixths of a circle and switches whole beams at a time, so following it exactly
// would snap two segments sideways; easing turns that into a head turning.
func follower(p Palette, r Room) Frame {
	const (
		// How wide the glow is at its narrowest and widest, in segments.
		narrow = 1.2
		wide   = 3.2

		// How far towards a new direction one frame moves, and how long a frame is assumed to be. Slow
		// enough to read as turning, quick enough not to lag a conversation crossing the room.
		ease = 0.18
	)

	var at float64
	var known bool

	return func(time.Duration) []Color {
		x := level(r)

		if want, ok := facing(r); ok {
			switch {
			case !known:
				at, known = want, true
			default:
				// Round the ring the short way, or turning past segment 11 would sweep the long way home.
				d := math.Mod(math.Mod(want-at, Segments)+Segments+Segments/2, Segments) - Segments/2
				at += d * ease
			}
		}
		if !known {
			// Nothing to point at yet: the meter says as much as can honestly be said.
			return arcOf(x, p)
		}

		// Both the width and the brightness follow the room, so silence is nothing at all rather than a
		// narrow dot at full strength.
		out := make([]Color, Segments)
		dot(out, at, p.Shade(x, x), narrow+(wide-narrow)*x)
		return out
	}
}
