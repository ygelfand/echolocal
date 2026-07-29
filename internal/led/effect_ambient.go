package led

import (
	"math"
	"time"
)

// ambientEffects are the ones that can be left running. Nothing here moves quickly, jumps, or draws
// the eye: a ring in the corner of a room is being looked at all evening, so an ambient effect is
// judged by whether it can be ignored, not by how much is happening.
var ambientEffects = []Effect{
	// In the ring's own colour.
	{Name: EffectPulse, New: breathe},
	{Name: EffectHeartbeat, New: heartbeat},
	{Name: EffectRipple, New: ripple},
	{Name: EffectStandingWave, New: standing},
	{Name: EffectTwinkle, New: twinkle},

	// In colours of their own, where the colours are the point: a flame is not a shape, and an
	// aurora in one colour is a ring that cannot make up its mind.
	{Name: EffectCrimsonHeartbeat, Palette: crimson, New: heartbeat},
	{Name: EffectAuroraPulse, Palette: aurora, New: breathe},
	{Name: EffectCandle, Palette: flame, New: flicker},
	{Name: EffectFireplace, Palette: fire, New: flicker},
	{Name: EffectEmbers, Palette: fire, New: embers},
	{Name: EffectAurora, Palette: aurora, New: drift},
	{Name: EffectSunsetDrift, Palette: sunset, New: drift},
	{Name: EffectOceanRipple, Palette: ocean, New: ripple},
	{Name: EffectRainbowTwinkle, Palette: wheel, New: twinkle},
	{Name: EffectForestTwinkle, Palette: forest, New: twinkle},
}

// breathe pulses the whole ring.
func breathe(p Palette) Frame {
	const period = 1200 * time.Millisecond

	return func(elapsed time.Duration) []Color {
		f := (1 - math.Cos(2*math.Pi*float64(elapsed%period)/float64(period))) / 2
		return around(p, f)
	}
}

// heartbeat thumps twice and rests, the way a pulse is felt rather than the way it looks on a
// monitor: a strong beat, a weaker one just behind it, then a gap long enough that the pair reads as
// one heartbeat instead of a fast blink.
//
// How hard the beat is takes the palette as well as the brightness, rather than spreading it round
// the ring: a heartbeat has no shape across the ring to spread anything over, and what it does have
// is force. So the strong beat climbs to the top of the palette and the weaker one behind it does not
// reach as far, which is the difference a colour can show and brightness alone cannot.
func heartbeat(p Palette) Frame {
	const period = 1400 * time.Millisecond

	// How quickly a thump dies away. Short, so the ring is dark for most of the period, which is
	// where the resting comes from.
	const decay = 170 * time.Millisecond

	beats := []struct {
		at time.Duration
		f  float64
	}{
		{0, 1},
		{200 * time.Millisecond, 0.6},
	}

	return func(elapsed time.Duration) []Color {
		t := elapsed % period

		var f float64
		for _, b := range beats {
			if t < b.at {
				continue
			}
			f = math.Max(f, b.f*math.Exp(-float64(t-b.at)/float64(decay)))
		}

		out := make([]Color, Segments)
		for i := range out {
			out[i] = p.Shade(f, f)
		}
		return out
	}
}

// ripple runs two crests round the ring over a low glow, slowly enough to read as water. Two,
// because a single crest travelling is a comet with soft edges; a second one opposite makes it read
// as a wave passing through the ring rather than something going round it.
//
// The palette is the depth of the water: a crest is drawn from the top of it and a trough from the
// bottom, so a gradient gives the wave something underneath it rather than only less light.
func ripple(p Palette) Frame {
	const (
		revolution = 3200 * time.Millisecond
		crests     = 2

		// What the troughs keep, so the ring stays present between crests instead of going out.
		glow = 0.12
	)

	return func(elapsed time.Duration) []Color {
		phase := float64(elapsed%revolution) / float64(revolution)

		out := make([]Color, Segments)
		for i := range out {
			// Cubed, which narrows the crest and widens the trough: an even swell around all twelve
			// segments looks like the ring breathing out of step with itself. Cubed rather than
			// squared because an even power is positive either side of the trough, and would put a
			// crest there too — twice as many as asked for.
			c := (1 + math.Cos(2*math.Pi*crests*(float64(i)/Segments-phase))) / 2
			c *= c * c
			out[i] = p.Shade(c, glow+(1-glow)*c)
		}
		return out
	}
}

// standing adds two waves going opposite ways round the ring. Where they meet in step the ring is
// brightest, where they cancel it is dark, and the difference from ripple is that the dark points
// stay where they are: the pattern breathes in place instead of travelling. Two crests each way put
// the still points on segments rather than between them, which twelve allows and most counts do not.
func standing(p Palette) Frame {
	const (
		period = 2600 * time.Millisecond
		crests = 2
		glow   = 0.08
	)

	return func(elapsed time.Duration) []Color {
		swell := math.Cos(2 * math.Pi * float64(elapsed%period) / float64(period))

		out := make([]Color, Segments)
		for i := range out {
			f := math.Abs(math.Sin(2*math.Pi*crests*float64(i)/Segments) * swell)
			out[i] = p.Shade(f, glow+(1-glow)*f)
		}
		return out
	}
}

// twinkle brightens segments one at a time on their own schedules, which is what makes it look
// random while keeping nothing between frames: each segment takes a period, a phase and a place in
// the palette from its index, and the periods do not divide each other, so the ring takes a long
// time to repeat itself.
func twinkle(p Palette) Frame {
	const (
		shortest = 1700 * time.Millisecond
		spread   = 1300 * time.Millisecond

		// How much of its own cycle a segment spends lit, and what it keeps the rest of the time.
		duty = 0.35
		dark = 0.05
	)

	sparks := make([]struct {
		period time.Duration
		phase  float64
		color  Color
	}, Segments)

	for i := range sparks {
		// Hashed rather than drawn from a source of randomness, so the ring twinkles the same way
		// after a restart and a test can say what it expects. The offset keeps segment 0 from
		// hashing to zero and starting every cycle exactly on time.
		h := hash(uint32(i) + 0x9E3779B9)
		sparks[i].period = shortest + time.Duration(float64(spread)*float64(h&0xFFFF)/0x10000)
		sparks[i].phase = float64(h>>16) / 0x10000
		sparks[i].color = p.At(sparks[i].phase)
	}

	return func(elapsed time.Duration) []Color {
		out := make([]Color, Segments)
		for i, s := range sparks {
			f := dark
			if x := math.Mod(float64(elapsed)/float64(s.period)+s.phase, 1); x < duty {
				f = math.Max(dark, math.Sin(math.Pi*x/duty))
			}
			out[i] = scale(s.color, f)
		}
		return out
	}
}

// flicker is a flame. The ring as a whole wanders in brightness while each segment wanders a little
// around it, so it moves as one thing rather than as twelve separate candles.
//
// With one colour that is a candle. With a palette from the base of a flame outwards it is a fire:
// the segment's own wander decides how deep into the flame it is at that moment, so the hot colours
// appear where it happens to be brightest, which is the difference between a fire and an orange ring
// going up and down.
func flicker(p Palette) Frame {
	return func(elapsed time.Duration) []Color {
		t := elapsed.Seconds()
		whole := 0.75 + 0.25*wander(t, 0)

		out := make([]Color, Segments)
		for i := range out {
			w := wander(t, float64(i)*0.7)
			f := whole * (0.8 + 0.2*w)

			// Along from the far end, because a flame palette runs hottest first: the brighter this
			// segment is at this moment, the further into the base of the flame it is. Sampled the other
			// way round it is still a fire of sorts, but the hot colours land on the dim segments, which
			// is the one thing a fire never does.
			out[i] = scale(p.Along(1-w), math.Min(1, f))
		}
		return out
	}
}

// embers is flicker with the occasional spark thrown off it. A fire does not only vary — every so
// often a piece of it flares brighter than the flame ever gets, and that is most of what tells a fire
// from an orange light being turned up and down.
//
// Sparks are rare enough that two rarely overlap, so each segment carries its own schedule from its
// hash, the same trick twinkle uses. What a spark does is reach past the top of the palette: it is
// the one thing here allowed to be brighter than the flame.
func embers(p Palette) Frame {
	base := flicker(p)

	const (
		// How often a given segment sparks, and how long one lasts. Twelve segments on this schedule
		// is a spark every second or so somewhere on the ring.
		every = 9 * time.Second
		lasts = 260 * time.Millisecond
	)

	phases := make([]float64, Segments)
	for i := range phases {
		phases[i] = float64(hash(uint32(i)+0x51ED2701)) / float64(1<<32)
	}

	return func(elapsed time.Duration) []Color {
		out := base(elapsed)
		for i, phase := range phases {
			x := math.Mod(float64(elapsed)/float64(every)+phase, 1)
			if x >= float64(lasts)/float64(every) {
				continue
			}

			// Up fast and down slower, which is what a spark does.
			f := x / (float64(lasts) / float64(every))
			f = math.Sin(math.Pi * math.Pow(f, 0.7))
			out[i] = add(out[i], scale(p.Along(0), 0.7*f))
		}
		return out
	}
}

// drift moves the palette round the ring unevenly and slowly, the way an aurora does: where a
// segment is looking in the palette is decided by two slow waves of different rates, so bands form,
// stretch and fold instead of marching past. A third decides brightness, so they fade in and out
// where they are rather than only sliding.
func drift(p Palette) Frame {
	return func(elapsed time.Duration) []Color {
		t := elapsed.Seconds()

		out := make([]Color, Segments)
		for i := range out {
			pos := float64(i) / Segments
			x := pos + 0.03*t +
				0.10*math.Sin(2*math.Pi*(0.07*t+pos)) +
				0.06*math.Sin(2*math.Pi*(0.13*t+2*pos))
			f := 0.45 + 0.55*(0.5+0.5*math.Sin(2*math.Pi*(0.11*t+1.5*pos)))
			out[i] = scale(p.At(x), f)
		}
		return out
	}
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
