package noise

import "math"

const NameBrook = "Brook"

// A bubble rings at Minnaert's frequency, 3.26 over its radius in metres, so three millimetres sounds
// near a kilohertz. As it collapses the radius shrinks and the pitch rises, and that rise is what the
// ear hears as water rather than as a bell.
const (
	// Radii in metres. Small: a millimetre rings at three kilohertz and four at eight hundred, and
	// anything fatter than that is a plughole rather than a stream.
	brookFine, brookFat = 0.0008, 0.004
	minnaert            = 3.26

	// A bubble is a tone, and a tone that lasts is a whistle. These are short enough to be heard as
	// water and the rise is what stops them being bells.
	brookRise             = 0.12
	brookQuick, brookSlow = 0.004, 0.018

	brookBubbles = 90
	brookMost    = 16

	// The bed is most of what a brook is; the bubbles are what tells you it is not a tap. Brighter than
	// rain: water over stones a few feet away, not a shower on a roof.
	brookBed    = 900
	brookHiss   = 1.0
	brookAccent = 0.35
)

type bubble struct {
	phase, step, rise float32
	amp, fade         float32
	left              int
}

func newBubble(g *Gen) bubble {
	radius := g.between(brookFine, brookFat)
	decay := g.between(brookQuick, brookSlow)
	life := int(decay * 2 * float32(g.Rate))
	amp := g.between(0, 1)

	return bubble{
		step: minnaert / radius / float32(g.Rate),
		rise: float32(math.Pow(1+brookRise, 1/float64(life))),
		amp:  amp * amp,
		fade: float32(math.Exp(-1 / float64(decay*float32(g.Rate)))),
		left: life,
	}
}

func (b *bubble) next() float32 {
	b.phase += b.step
	if b.phase >= 1 {
		b.phase--
	}
	b.step *= b.rise
	b.amp *= b.fade
	b.left--

	return b.amp * sine(b.phase)
}

var brookSound = Sound{
	Name: NameBrook,
	RMS:  0.98812,
	New: func(g *Gen) Fill {
		var (
			bed     pink
			low     lowpass
			cut     = onePole(brookBed, g.Rate)
			bubbles = make([]bubble, 0, brookMost)
		)

		return func(dst []float32) {
			for i := range dst {
				p := bed.next(g)
				out := (p - low.run(p, cut)) * brookHiss

				if g.chance(brookBubbles) && len(bubbles) < brookMost {
					bubbles = append(bubbles, newBubble(g))
				}

				for j := len(bubbles) - 1; j >= 0; j-- {
					out += bubbles[j].next() * brookAccent

					if bubbles[j].left <= 0 {
						bubbles[j] = bubbles[len(bubbles)-1]
						bubbles = bubbles[:len(bubbles)-1]
					}
				}
				dst[i] = out
			}
		}
	},
}
