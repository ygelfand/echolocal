package noise

import "math"

const NameOcean = "Ocean"

// Every wave has to be a different length: hold the period steady and this stops being water and
// becomes a machine breathing.
const (
	oceanWave = 11.0
	oceanVary = 0.3

	oceanTrough, oceanBreak = 250.0, 1500.0
	oceanFloor              = 0.3
	oceanSpray              = 0.35
)

var oceanSound = Sound{
	Name: NameOcean,
	RMS:  0.3142,
	New: func(g *Gen) Fill {
		var (
			bed   brown
			foam  pink
			swell lowpass

			period = g.between(oceanWave*(1-oceanVary), oceanWave*(1+oceanVary))
			phase  float32
			env    float32
			n      int
		)

		return func(dst []float32) {
			for i := range dst {
				phase += 1 / (period * float32(g.Rate))
				if phase >= 1 {
					phase--
					period = g.between(oceanWave*(1-oceanVary), oceanWave*(1+oceanVary))
				}

				// Cubed sine: a swell that leans towards its break rather than a symmetrical hump.
				if n%ctrl == 0 {
					s := float32(math.Sin(math.Pi * float64(phase)))
					env = s * s * s
				}
				n++

				out := swell.run(bed.next(g), corner(oceanTrough+(oceanBreak-oceanTrough)*env, g.Rate))
				dst[i] = out*(oceanFloor+(1-oceanFloor)*env) + oceanSpray*foam.next(g)*env*env
			}
		}
	},
}
