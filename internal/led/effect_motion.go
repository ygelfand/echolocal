package led

import (
	"math"
	"time"
)

// motionEffects travel. Something going round the ring is how the device says it is busy, so these
// are the ones a conversation runs, and they are the ones worth looking at for direction: the driver
// can play any of them backwards, which is how listening and waiting are told apart.
var motionEffects = []Effect{
	// In the ring's own colour.
	{Name: EffectComet, New: comet},
	{Name: EffectChase, New: chase},
	{Name: EffectScanner, New: scanner},
	{Name: EffectPinwheel, New: pinwheel},
	{Name: EffectSpiral, New: spiral},
	{Name: EffectWipe, New: wipe},
	{Name: EffectHelix, New: helix},
	{Name: EffectOrbits, New: orbits},
	{Name: EffectBounce, New: bounce},
	{Name: EffectSpring, New: spring},

	// In colours of their own.
	{Name: EffectRainbow, Palette: wheel, New: spin},
	{Name: EffectFireComet, Palette: fire, New: comet},
	{Name: EffectIceComet, Palette: ice, New: comet},
	{Name: EffectRainbowChase, Palette: wheel, New: chase},
	{Name: EffectIceScanner, Palette: ice, New: scanner},
	{Name: EffectRainbowPinwheel, Palette: wheel, New: pinwheel},
	{Name: EffectSunsetSpiral, Palette: sunset, New: spiral},
	{Name: EffectRainbowOrbits, Palette: wheel, New: orbits},
	{Name: EffectDNA, Palette: duo, New: helix},
	{Name: EffectPacMan, Palette: pellets, New: pacman},
}

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

// scanner sweeps an eye round the ring and back again. The glow around the head is symmetric, unlike
// the comet's tail, so the turn at each end reads as the eye slowing and coming back rather than a
// tail flipping to the other side. The palette runs outwards from the head, so a hot core with cool
// edges is a matter of which colours it is given.
func scanner(p Palette) Frame {
	const (
		// One pass across the ring, so a there-and-back takes twice this.
		sweep = 900 * time.Millisecond

		// How far the glow reaches, in segments.
		width = 2.2
	)

	return func(elapsed time.Duration) []Color {
		// A triangle wave: across, then back.
		x := math.Mod(float64(elapsed)/float64(sweep), 2)
		if x > 1 {
			x = 2 - x
		}
		head := x * (Segments - 1)

		out := make([]Color, Segments)
		for i := range out {
			d := ringDist(i, head)
			if d >= width {
				continue
			}
			f := 1 - d/width
			out[i] = p.Shade(d/width, f*f)
		}
		return out
	}
}

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

// spin turns the palette round the ring. It has no motion of its own beyond that, so it is only
// worth running with colours that have somewhere to go — a single colour spun looks like a ring that
// is simply on.
func spin(p Palette) Frame {
	const revolution = 1500 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		phase := float64(elapsed%revolution) / float64(revolution)
		out := make([]Color, Segments)
		for i := range out {
			out[i] = p.At(float64(i)/Segments + phase)
		}
		return out
	}
}

// spiral runs a brightness gradient round the ring and turns it. Where the comet is a head with a
// short tail, this is lit the whole way round and only says which way it is going, which makes it the
// calmest way the ring can show that something is turning.
func spiral(p Palette) Frame {
	const revolution = 2200 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		phase := float64(elapsed%revolution) / float64(revolution)

		out := make([]Color, Segments)
		for i := range out {
			// How far behind the leading edge this segment is, as a fraction of the way round.
			behind := math.Mod(float64(i)/Segments-phase+1, 1)
			f := 1 - behind
			out[i] = p.Shade(f, f)
		}
		return out
	}
}

// wipe fills the ring from the front and empties it again. It is the shape of something being
// counted out rather than something travelling, which is why it fills all the way before it turns
// round: a wipe that reverses part way looks like it changed its mind.
func wipe(p Palette) Frame {
	const period = 2400 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		x := 2 * float64(elapsed%period) / float64(period)
		if x > 1 {
			x = 2 - x
		}
		return Arc(x, p.Along(x))
	}
}

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

// orbits turns three dots at speeds that do not divide each other, so they pass and re-pass without
// ever settling into a pattern. Marbles knocking about a ring would look much the same: identical
// marbles that collide elastically swap velocities, which is indistinguishable from passing through
// each other, so there is nothing to simulate.
func orbits(p Palette) Frame {
	speeds := []float64{1, -0.62, 0.37}
	const revolution = 2600 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		t := float64(elapsed) / float64(revolution)

		out := make([]Color, Segments)
		for i, speed := range speeds {
			dot(out, math.Mod(t*speed, 1)*Segments, p.Nth(i), 1.4)
		}
		return out
	}
}

// bounce drops a ball, which keeps a fraction of its height each time it lands until there is nothing
// left to bounce and it is dropped again.
//
// Each bounce is a parabola, so the ball is quick through the bottom and slow at the top, which is
// the part that reads as weight. A bounce's duration goes with the square root of its height, so the
// bounces shorten as they shrink on their own — nothing here has to say how long each one lasts. The
// whole sequence is worked out once and looked up, which keeps the frame function free of state and
// so still reversible and still restartable.
func bounce(p Palette) Frame {
	const (
		first = 900 * time.Millisecond
		keeps = 0.55

		// Below this there is no bounce left to see, and the ring rests before the next drop.
		spent = 0.04
		rest  = 700 * time.Millisecond
	)

	type arc struct {
		at     time.Duration
		d      time.Duration
		height float64
	}

	var arcs []arc
	var cycle time.Duration
	for h := 1.0; h > spent; h *= keeps {
		d := time.Duration(float64(first) * math.Sqrt(h))
		arcs = append(arcs, arc{at: cycle, d: d, height: h})
		cycle += d
	}
	cycle += rest

	return func(elapsed time.Duration) []Color {
		t := elapsed % cycle

		var at float64
		for _, a := range arcs {
			if t < a.at || t >= a.at+a.d {
				continue
			}
			// A parabola through the two ends of the bounce, peaking at its height in the middle.
			x := 2*float64(t-a.at)/float64(a.d) - 1
			at = a.height * (1 - x*x) * (Segments - 1)
			break
		}

		out := make([]Color, Segments)
		dot(out, at, p.Along(at/(Segments-1)), 1.5)
		return out
	}
}

// spring stretches an arc out, then lets it go. What makes it a spring rather than an animation
// easing to a stop is that it passes where it is heading and comes back: each half overshoots and
// rings down around where it lands.
func spring(p Palette) Frame {
	const (
		// Out, then back. Each half is one damped step.
		half = 1300 * time.Millisecond

		// Overshoots per half, and how fast they die away. Ringing much longer than it takes to
		// settle is a wobble; dying much faster and the overshoot never shows at all.
		rings = 1.5
		decay = 3.0

		// Where each half is heading. Short of the ends on purpose: an overshoot past a full ring or
		// an empty one is clipped, and a spring that cannot overshoot is just a wipe.
		stretched = 0.72
		slack     = 0.12
	)

	// step is a damped approach from one to the other across a half, x running 0 to 1.
	step := func(from, to, x float64) float64 {
		return to + (from-to)*math.Exp(-decay*x)*math.Cos(2*math.Pi*rings*x)
	}

	return func(elapsed time.Duration) []Color {
		t := elapsed % (2 * half)

		var x float64
		if t < half {
			x = step(slack, stretched, float64(t)/float64(half))
		} else {
			x = step(stretched, slack, float64(t-half)/float64(half))
		}

		x = math.Max(0, math.Min(1, x))
		return Arc(x, p.Along(x))
	}
}

// pacman eats his way round the ring and starts again. What makes him recognisable is not the shape
// of him, which twelve segments cannot draw, but that the ring ahead is dotted and the ring behind is
// dark: something is being consumed rather than merely passing by.
func pacman(p Palette) Frame {
	const (
		lap = 3600 * time.Millisecond

		// The chomp. Fast enough to read as a mouth, not so fast it looks like a fault.
		chomp = 220 * time.Millisecond
	)

	return func(elapsed time.Duration) []Color {
		at := float64(elapsed%lap) / float64(lap) * Segments

		out := make([]Color, Segments)

		// Pellets on every other segment he has not reached yet. Every other, because a pellet on
		// every one leaves nothing between them and reads as a lit ring.
		for i := range Segments {
			if i%2 == 0 && float64(i) > at {
				out[i] = p.Nth(1)
			}
		}

		open := math.Abs(math.Sin(math.Pi * float64(elapsed%chomp) / float64(chomp)))
		dot(out, at, scale(p.Nth(0), 0.55+0.45*(1-open)), 1.3)
		return out
	}
}

// dot adds a glow centred on a fractional position round the ring. Added rather than assigned, so
// that two dots crossing brighten each other for a frame instead of one erasing the other.
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

// ringDist is how far segment i is from a position on the ring, measured the short way round so that
// a glow spans the seam between segment 11 and segment 0 like any other pair of neighbours.
func ringDist(i int, at float64) float64 {
	d := math.Abs(float64(i) - math.Mod(math.Mod(at, Segments)+Segments, Segments))
	return math.Min(d, Segments-d)
}
