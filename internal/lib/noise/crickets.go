package noise

import "math"

const NameCrickets = "Crickets"

const (
	cricketLow, cricketHigh = 3800.0, 5200.0
	cricketRing             = 0.004

	chirpLength, chirpGap = 0.022, 0.030
	chirpsLow, chirpsHigh = 3, 6
	restLow, restHigh     = 0.5, 2.2

	crickets = 3
)

// Levelled by its peak: this is mostly silence, and bringing the average up to where a steady sound
// sits would mean chirps at nearly full scale.
var cricketSound = Sound{
	Name: NameCrickets,
	Peak: 0.18562,
	New: func(g *Gen) Fill {
		field := make([]cricket, crickets)
		for i := range field {
			field[i] = newCricket(g)
		}

		return func(dst []float32) {
			for i := range dst {
				var out float32
				for j := range field {
					out += field[j].next(g)
				}
				dst[i] = out
			}
		}
	},
}

type cricket struct {
	tone reson

	chirping    bool
	left, chirp int
	burst       int
}

func newCricket(g *Gen) cricket {
	return cricket{
		tone: newReson(g.between(cricketLow, cricketHigh), cricketRing, g.Rate),
		left: int(g.between(restLow, restHigh) * float32(g.Rate)),
	}
}

func (c *cricket) next(g *Gen) float32 {
	c.left--
	if c.left <= 0 {
		c.turn(g)
	}
	if !c.chirping {
		return 0
	}

	// Raised sine, so a chirp opens and closes instead of clicking at both ends.
	at := 1 - float32(c.left)/float32(c.chirp)
	return c.tone.run(g.white() * float32(math.Sin(math.Pi*float64(at))))
}

func (c *cricket) turn(g *Gen) {
	switch {
	case c.chirping:
		c.chirping = false
		c.left = int(chirpGap * float32(g.Rate))

	case c.burst > 0:
		c.burst--
		c.chirping = true
		c.chirp = int(chirpLength * float32(g.Rate))
		c.left = c.chirp

	default:
		c.burst = int(g.between(chirpsLow, chirpsHigh))
		c.left = int(g.between(restLow, restHigh) * float32(g.Rate))
	}
}
