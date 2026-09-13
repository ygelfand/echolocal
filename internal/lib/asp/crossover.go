package asp

import "math"

// crossover splits a signal into bands that sum back to what went in.
//
// Each split is Linkwitz-Riley: two identical Butterworth sections, so the low and high halves sum to
// an allpass rather than to unity. A tree of them would leave each band having passed through a
// different set of those allpasses and no longer summing flat, so every band is also run through the
// allpass of the splits it missed. Magnitudes are untouched by that and the sum comes back flat.
type crossover struct {
	splits []*split
}

// split is one crossover frequency: the low and high halves, and the allpass that the bands on the
// other side of the tree need to be put through to stay in step with it.
type split struct {
	low, high   *lr4
	compensated []int
	allpass     []*biquad
}

// newCrossover builds a split at each frequency, ascending. The bands it produces are in the same
// order: below the first frequency, between each pair, and above the last.
func newCrossover(fc []float64, rate int) *crossover {
	c := &crossover{}
	for i, f := range fc {
		s := &split{low: newLR4(f, rate, false), high: newLR4(f, rate, true)}

		// A band is finished by the split that peels it off and never sees the splits after it, so it
		// has to be put through their allpasses to come back in step. This split's allpass therefore
		// belongs to every band peeled off before it.
		for b := 0; b < i; b++ {
			s.compensated = append(s.compensated, b)
			s.allpass = append(s.allpass, newAllpass(f, rate))
		}
		c.splits = append(c.splits, s)
	}
	return c
}

func (c *crossover) reset() {
	for _, s := range c.splits {
		for _, f := range []*biquad{s.low.a, s.low.b, s.high.a, s.high.b} {
			f.z1, f.z2 = 0, 0
		}
		for _, f := range s.allpass {
			f.z1, f.z2 = 0, 0
		}
	}
}

// process fills out with one band each. The input is left alone.
func (c *crossover) process(x []float32, out [][]float32) {
	rest := out[len(out)-1]
	copy(rest, x)

	// Peel the lowest band off what is left, repeatedly, so each split only ever sees the part of the
	// spectrum above the ones before it.
	for i, s := range c.splits {
		band := out[i]
		for j, v := range rest {
			band[j] = s.low.step(v)
			rest[j] = s.high.step(v)
		}
	}

	// Put the bands already peeled off through the allpass of each later split, so that what comes
	// back is the same signal delayed the same way rather than a comb.
	for _, s := range c.splits {
		for k, b := range s.compensated {
			ap := s.allpass[k]
			band := out[b]
			for j, v := range band {
				band[j] = ap.step(v)
			}
		}
	}
}

// lr4 is a fourth-order Linkwitz-Riley section: one Butterworth pair run twice.
type lr4 struct{ a, b *biquad }

func newLR4(fc float64, rate int, high bool) *lr4 {
	return &lr4{a: newButterworth(fc, rate, high), b: newButterworth(fc, rate, high)}
}

func (l *lr4) step(x float32) float32 { return l.b.step(l.a.step(x)) }

// biquad is a direct form II transposed section.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	z1, z2             float64
}

func (f *biquad) step(x float32) float32 {
	in := float64(x)
	out := f.b0*in + f.z1
	f.z1 = f.b1*in - f.a1*out + f.z2
	f.z2 = f.b2*in - f.a2*out
	return float32(out)
}

// newButterworth is a second-order section at Q of a half root two, low or high pass.
func newButterworth(fc float64, rate int, high bool) *biquad {
	w := 2 * math.Pi * fc / float64(rate)
	cos, sin := math.Cos(w), math.Sin(w)
	alpha := sin / (2 * math.Sqrt2 / 2)
	a0 := 1 + alpha

	f := &biquad{a1: -2 * cos / a0, a2: (1 - alpha) / a0}
	if high {
		f.b0 = (1 + cos) / 2 / a0
		f.b1 = -(1 + cos) / a0
		f.b2 = f.b0
		return f
	}
	f.b0 = (1 - cos) / 2 / a0
	f.b1 = (1 - cos) / a0
	f.b2 = f.b0
	return f
}

// newAllpass is the second-order allpass a Linkwitz-Riley pair sums to.
func newAllpass(fc float64, rate int) *biquad {
	w := 2 * math.Pi * fc / float64(rate)
	cos, sin := math.Cos(w), math.Sin(w)
	alpha := sin / (2 * math.Sqrt2 / 2)
	a0 := 1 + alpha

	return &biquad{
		b0: (1 - alpha) / a0,
		b1: -2 * cos / a0,
		b2: 1,
		a1: -2 * cos / a0,
		a2: (1 - alpha) / a0,
	}
}
