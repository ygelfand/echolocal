package noise

import "math"

// reson is a two-pole resonator, the filter most of these sounds are built from: a room mode, a
// fuselage, the band a gust lives in, the pitch of a droplet.
type reson struct {
	a1, a2 float32
	y1, y2 float32

	// drive is what an input is scaled by to come out at the level it went in. The denominator factors
	// to (1-r)·|1 - r·e^-2jω|, so the gain depends on where the resonance sits as much as on how long it
	// rings: with (1-r) alone a droplet at 800 Hz is six times louder than one at five kilohertz, and a
	// filter being swept changes level as it moves.
	drive float32
}

// newReson places one at a frequency, ringing down by 1/e in decay seconds.
func newReson(freq, decay float32, rate int) reson {
	r := math.Exp(-1 / float64(decay*float32(rate)))
	w := 2 * math.Pi * float64(freq) / float64(rate)

	return reson{
		a1:    float32(2 * r * math.Cos(w)),
		a2:    float32(-r * r),
		drive: float32((1 - r) * math.Sqrt(1-2*r*math.Cos(2*w)+r*r)),
	}
}

func (o *reson) run(x float32) float32 {
	y := o.a1*o.y1 + o.a2*o.y2 + x*o.drive
	o.y2, o.y1 = o.y1, y
	return y
}

// burst is a resonator fed noise through an envelope that falls away: one short event with a pitch
// but not a note. Several are alive at once and each is a couple of multiplies, which is what makes
// hundreds a second affordable.
type burst struct {
	reson
	env, fade float32
	left      int
}

// newBurst lives three decays, by which point a twentieth of it is left and the bed it sits under has
// covered the rest. It is what decides the cost of a shower of these: two decays fewer is a third off
// how many are ringing at once.
func newBurst(freq, ring, decay, amp float32, rate int) burst {
	return burst{
		reson: newReson(freq, ring, rate),
		env:   amp,
		fade:  float32(math.Exp(-1 / float64(decay*float32(rate)))),
		left:  int(decay * 3 * float32(rate)),
	}
}

func (b *burst) run(w float32) float32 {
	y := b.reson.run(w * b.env)
	b.env *= b.fade
	b.left--
	return y
}

func (b *burst) done() bool { return b.left <= 0 }

// bursts bounds what a sound made of events costs: a run of bad luck loses one nobody could have
// picked out of the hundred around it.
type bursts struct {
	live []burst
	most int
}

func newBursts(most int) bursts { return bursts{live: make([]burst, 0, most), most: most} }

func (p *bursts) add(b burst) {
	if len(p.live) < p.most {
		p.live = append(p.live, b)
	}
}

// sum runs every live event. They share one noise sample rather than drawing their own: each is behind
// its own narrow resonance, so what comes out is uncorrelated anyway, and drawing thirty a sample is
// most of what a shower of droplets costs.
func (p *bursts) sum(g *Gen) float32 {
	w := g.white()

	var out float32
	for i := len(p.live) - 1; i >= 0; i-- {
		out += p.live[i].run(w)

		if p.live[i].done() {
			p.live[i] = p.live[len(p.live)-1]
			p.live = p.live[:len(p.live)-1]
		}
	}
	return out
}
