package led

import "time"

// roomMeter fills the ring with the room, a twelfth at a time, which is the one thing twelve segments
// in a circle are unarguably good at. The leading segment dims by the remainder, so the last twelfth
// still moves — the same trick the volume arc uses.
//
// A palette runs along the fill rather than round it, so the far end of the meter is the loud end.
func roomMeter(p Palette, r Room) Frame {
	return func(time.Duration) []Color {
		return arcOf(level(r), p)
	}
}
