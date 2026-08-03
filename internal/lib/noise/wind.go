package noise

import "math"

const NameWind = "Wind"

// Wind is broadband with a moving emphasis, not a moving band: everything through one narrow
// resonance is a whistle, however much the whistle wanders.
const (
	// windAir is where the bed is cut, and windBand how much of the emphasis is mixed back over it.
	windAir  = 2200.0
	windBand = 0.7

	// Where the emphasis sits and how wide it is. Wide: a couple of hundred hertz, not a tone.
	windLow, windHigh    = 300.0, 1400.0
	windWander           = 4.0
	windLoose, windTight = 0.0008, 0.002
	windShape            = 5.0

	windLull, windGust = 0.45, 1.0
	windBreath         = 7.0
)

var windSound = Sound{
	Name: NameWind,
	RMS:  1.5228,
	New: func(g *Gen) Fill {
		var (
			bed             pink
			air             lowpass
			cut             = onePole(windAir, g.Rate)
			band            = newReson((windLow+windHigh)/2, (windLoose+windTight)/2, g.Rate)
			centre          = newWalk(windLow, windHigh, windWander, g.Rate)
			ring            = newWalk(windLoose, windTight, windShape, g.Rate)
			gust            = newWalk(windLull, windGust, windBreath, g.Rate)
			level   float32 = 1
			n       int
		)

		return func(dst []float32) {
			for i := range dst {
				// The walks move every sample so they stay smooth; the filter is rebuilt at the control
				// rate, since that is what costs a cosine and an exponential. The bed falls 3 dB an octave,
				// so the emphasis is compensated for where it has wandered to.
				f, r := centre.next(g), ring.next(g)
				if n%ctrl == 0 {
					band = newReson(f, r, g.Rate)
					level = float32(math.Sqrt(float64(f / windLow * (r / windLoose))))
				}
				n++

				x := air.run(bed.next(g), cut)
				dst[i] = (x + windBand*level*band.run(x)) * gust.next(g)
			}
		}
	},
}
