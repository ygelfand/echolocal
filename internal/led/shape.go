package led

import (
	"math"
	"time"
)

// What the effects draw with. Every one of these is used by more than one of them, which is the only
// reason it is here rather than in the file of the effect that wants it.

// around lays a palette round the ring at one brightness, which is what an effect that has no motion of
// its own across the segments wants: with one colour it is the whole ring in that colour, with a palette
// it is that palette spread evenly.
func around(p Palette, f float64) []Color {
	out := make([]Color, Segments)
	for i := range out {
		out[i] = scale(p.At(float64(i)/Segments), f)
	}
	return out
}

// shaded is the other way to put one number on the whole ring: rather than spreading the palette across
// the segments, every segment sits at the same point in it and climbs as f rises. For an effect whose
// shape is how hard something is happening rather than where.
func shaded(p Palette, f float64) []Color {
	out := make([]Color, Segments)
	for i := range out {
		out[i] = p.Shade(f, f)
	}
	return out
}

// dot adds a glow centred on a fractional position round the ring. Added rather than assigned, so that
// two dots crossing brighten each other for a frame instead of one erasing the other.
func dot(out []Color, at float64, c Color, width float64) {
	for i := range out {
		d := ringDist(i, at)
		if d >= width {
			continue
		}
		f := 1 - d/width
		out[i] = add(out[i], scale(c, f*f))
	}
}

// ringDist is how far segment i is from a position on the ring, measured the short way round so that a
// glow spans the seam between segment 11 and segment 0 like any other pair of neighbours.
func ringDist(i int, at float64) float64 {
	d := math.Abs(float64(i) - math.Mod(math.Mod(at, Segments)+Segments, Segments))
	return math.Min(d, Segments-d)
}

// wander is a smooth 0-1 signal from three sines whose rates do not divide each other, which is a
// flicker that never quite repeats and, unlike noise, needs nothing carried between frames.
func wander(t, offset float64) float64 {
	s := math.Sin(2*math.Pi*(1.7*t+offset)) +
		math.Sin(2*math.Pi*(2.9*t+1.3*offset)) +
		math.Sin(2*math.Pi*(4.7*t+0.6*offset))
	return (s/3 + 1) / 2
}

// hash mixes an integer into one that looks unrelated to its neighbours, so effects can be given
// per-segment variation without carrying any state.
func hash(x uint32) uint32 {
	x ^= x >> 16
	x *= 0x7FEB352D
	x ^= x >> 15
	x *= 0x846CA68B
	x ^= x >> 16
	return x
}

// level reads the room and keeps it inside 0 to 1, so an effect can trust what it is handed. A nil level
// cannot happen through the catalogue, which refuses to build a room effect without a source, but a zero
// reads as a quiet room rather than as a panic.
func level(l Level) float64 {
	if l == nil {
		return 0
	}
	return math.Max(0, math.Min(1, l()))
}

// byRoom turns a motion into a room effect: it runs as written and the room decides how much of it
// shows, which in silence is none of it. What that buys over a plain effect is that the ring is dark and
// silent until something happens, and then it is the animation rather than a meter — a wheel that
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
