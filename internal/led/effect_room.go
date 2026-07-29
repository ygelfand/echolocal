package led

import (
	"math"
	"time"
)

// roomEffects react to the room rather than to the clock. They are what a device that is always
// listening can honestly show: not a guess at what is happening, just how loud it is in here.
//
// None of them can be a light effect, because none of them mean anything without something feeding
// them. They are chosen from their own control and are simply on while chosen.
var roomEffects = []Effect{
	{Name: EffectRoomGlow, Senses: roomGlow},
	{Name: EffectRoomMeter, Senses: roomMeter},
	{Name: EffectRoomOcean, Palette: ocean, Senses: roomMeter},
	{Name: EffectRoomVU, Palette: vu, Senses: roomMeter},
	{Name: EffectRoomFire, Palette: fire, Senses: roomFire},
	{Name: EffectRoomSpin, Palette: wheel, Senses: roomSpin},

	// Any motion in the catalogue can be a room effect, since all the room has to do is decide how
	// much of it shows. These are the ones worth offering rather than all of them.
	{Name: EffectRoomAurora, Palette: aurora, Senses: byRoom(drift)},
	{Name: EffectRoomTwinkle, Palette: wheel, Senses: byRoom(twinkle)},
	{Name: EffectRoomEmbers, Palette: fire, Senses: byRoom(embers)},
}

// byRoom turns a motion into a room effect: it runs as written and the room decides how much of it
// shows, which in silence is none of it. What that buys over a plain effect is that the ring is dark
// and silent until something happens, and then it is the animation rather than a meter — a wheel that
// appears while people are talking and fades out when the room settles.
func byRoom(build func(Palette) Frame) func(Palette, Level) Frame {
	return func(p Palette, l Level) Frame {
		frame := build(p)

		return func(elapsed time.Duration) []Color {
			x := level(l)
			out := frame(elapsed)
			for i := range out {
				out[i] = scale(out[i], x)
			}
			return out
		}
	}
}

// Nothing here rests lit. A dark ring does read as broken rather than as calm, which is why all three
// of these kept a little light at the bottom to begin with — but on this hardware a lit segment whines
// audibly across the room, so an effect meant to be left on all evening has to be silent when nothing
// is happening, and the only silent light is none.
//
// That puts the weight on the level actually reaching zero, which against a floor that follows the
// quietest recent moment it may well not: see internal/mic/level.go.

// roomGlow brightens the whole ring with the room, and goes out with it.
func roomGlow(p Palette, l Level) Frame {
	return func(time.Duration) []Color {
		return around(p, level(l))
	}
}

// roomMeter fills the ring with the room, a twelfth at a time, which is the one thing twelve segments
// in a circle are unarguably good at. The leading segment dims by the remainder, so the last
// twelfth still moves — the same trick the volume arc uses.
//
// A palette runs along the fill rather than round it, so the far end of the meter is the loud end.
func roomMeter(p Palette, l Level) Frame {
	return func(time.Duration) []Color {
		return arcOf(level(l), p)
	}
}

// roomFire is a fire the room feeds: it goes out in silence, catches at a word and burns while people
// are talking. The flicker underneath means it is never a still picture of a fire while it is lit.
//
// The room sets how far up the flame the ring may reach and the flicker moves underneath that, which
// is what keeps the two readable at once: a little noise is dim and dark red, a room full of talking is
// bright and nearly white, and the difference is a colour rather than only a brightness.
func roomFire(p Palette, l Level) Frame {
	// How far down the palette a barely-lit fire may reach. The far end of a flame palette is nearly
	// black on purpose, because in a full fire it sits next to the bright parts; on its own it needs
	// something under it or the first flicker of a fire is invisible.
	const coolest = 0.1

	return func(elapsed time.Duration) []Color {
		x := level(l)
		t := elapsed.Seconds()
		whole := 0.75 + 0.25*wander(t, 0)

		out := make([]Color, Segments)
		for i := range out {
			w := wander(t, float64(i)*0.7)

			// Sampled from the far end, because a flame palette runs hottest first.
			heat := (coolest + (1-coolest)*x) * (0.55 + 0.45*w)
			f := x * whole * (0.8 + 0.2*w)
			out[i] = scale(p.Along(1-heat), math.Min(1, f))
		}
		return out
	}
}

// roomSpin turns the palette round the ring at a speed the room sets: still and dark in silence,
// turning as fast as the room is loud. The phase is carried between frames rather than worked out
// from elapsed, because a speed that changes has to be integrated — there is no closed form for
// "however fast the room happened to be".
func roomSpin(p Palette, l Level) Frame {
	const revolution = 1400 * time.Millisecond

	var phase float64
	var last time.Duration

	return func(elapsed time.Duration) []Color {
		x := level(l)

		dt := elapsed - last
		last = elapsed
		if dt < 0 || dt > time.Second {
			dt = 0
		}
		phase += x * dt.Seconds() * float64(time.Second) / float64(revolution)

		out := make([]Color, Segments)
		for i := range out {
			out[i] = scale(p.At(float64(i)/Segments+phase), x)
		}
		return out
	}
}

// level reads the room and keeps it inside 0 to 1, so an effect can trust what it is handed. A nil
// level cannot happen through the catalogue, which refuses to build one of these without a source,
// but a zero reads as a quiet room rather than as a panic.
func level(l Level) float64 {
	if l == nil {
		return 0
	}
	return math.Max(0, math.Min(1, l()))
}
