package noise

import (
	"math"
	"math/rand/v2"
)

// Gen is what every sound is built from. It holds no filter state — that belongs to the sound, which
// is what lets two of them run at once without sharing anything.
type Gen struct {
	Rate int

	rand *rand.Rand
}

func newRand(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed>>32)) }

// white is uniform over -1 to 1, whose spectrum is as flat as a gaussian's for a tenth of the work.
func (g *Gen) white() float32 { return g.rand.Float32()*2 - 1 }

func (g *Gen) between(lo, hi float32) float32 { return lo + g.rand.Float32()*(hi-lo) }

// chance reports whether something happening this many times a second happens on this sample.
func (g *Gen) chance(perSecond float32) bool {
	return g.rand.Float32() < perSecond/float32(g.Rate)
}

// ctrl is how many samples pass between recalculating a filter being swept. A wind that changes its
// mind 750 times a second is indistinguishable from one that does it every sample.
const ctrl = 64

// walk is a value that wanders: a target re-rolled every so often, approached rather than jumped to.
// Every one of these sounds needs at least one, since a level that never moves is what gives a
// generated sound away.
type walk struct {
	value, target float32
	lo, hi        float32
	rate          float32
	coef          float32
}

// newWalk wanders between lo and hi, taking about period seconds to get anywhere and re-aiming about
// that often.
func newWalk(lo, hi, period float32, rate int) walk {
	mid := (lo + hi) / 2
	return walk{
		value: mid, target: mid,
		lo: lo, hi: hi,
		rate: 1 / period,
		coef: onePole(1/period, rate),
	}
}

func (w *walk) next(g *Gen) float32 {
	if g.chance(w.rate) {
		w.target = g.between(w.lo, w.hi)
	}
	w.value += (w.target - w.value) * w.coef
	return w.value
}

// lowpass is a one-pole filter whose corner can be moved while it runs.
type lowpass float32

func (l *lowpass) run(x, coef float32) float32 {
	*l += lowpass((x - float32(*l)) * coef)
	return float32(*l)
}

// comb is a short delay fed back on itself: the hollow note air takes on going through a grille.
type comb struct {
	buf      []float32
	at       int
	feedback float32
}

func newComb(delay, feedback float32, rate int) comb {
	return comb{buf: make([]float32, max(1, int(delay*float32(rate)))), feedback: feedback}
}

func (c *comb) run(x float32) float32 {
	y := x + c.buf[c.at]*c.feedback
	c.buf[c.at] = y
	c.at = (c.at + 1) % len(c.buf)
	return y
}

// sineSize is the resolution of the table below. Interpolated, it is accurate to a hundred thousandth,
// and it exists because a brook asks for a dozen sines a sample: on this device the library call is
// twenty times the cost of the whole rest of the sound.
const sineSize = 1024

var sineTable = func() [sineSize + 1]float32 {
	var t [sineSize + 1]float32
	for i := range t {
		t[i] = float32(math.Sin(2 * math.Pi * float64(i) / sineSize))
	}
	return t
}()

// sine takes a phase from 0 to 1 rather than radians, since that is what a phase accumulator holds.
func sine(phase float32) float32 {
	x := phase * sineSize
	i := int(x)
	f := x - float32(i)
	return sineTable[i] + (sineTable[i+1]-sineTable[i])*f
}

// onePole is the coefficient for a filter that follows its input with a corner at freq.
func onePole(freq float32, rate int) float32 {
	return 1 - float32(math.Exp(-2*math.Pi*float64(freq)/float64(rate)))
}

// corner is onePole without the exponential, for a filter being swept: within a fraction of a dB
// wherever the corner is well under the rate, which for everything here it is.
func corner(freq float32, rate int) float32 {
	return min(1, 2*math.Pi*freq/float32(rate))
}
