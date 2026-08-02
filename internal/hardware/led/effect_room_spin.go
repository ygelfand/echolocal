package led

import "time"

// roomSpin turns the palette round the ring at a speed the room sets: still and dark in silence,
// turning as fast as the room is loud. The phase is carried between frames rather than worked out from
// elapsed, because a speed that changes has to be integrated — there is no closed form for "however
// fast the room happened to be".
func roomSpin(p Palette, r Room) Frame {
	const revolution = 1400 * time.Millisecond

	var phase float64
	var last time.Duration

	return func(elapsed time.Duration) []Color {
		x := level(r)

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
