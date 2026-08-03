package noise

const NameRain = "Rain"

// A bed of far drops with near ones on top. Neither alone works: the bed is pink noise, and the
// droplets on their own are a dripping tap.
const (
	// Droplets a second. Few enough to be heard one at a time: past a hundred or so they stop being
	// impacts and turn back into the hiss they are sitting on.
	rainFall = 55

	rainLow, rainHigh   = 900, 4500
	rainQuick, rainSlow = 0.004, 0.014

	// How long a droplet's own resonance rings. Longer is narrower, which is what makes an impact
	// sound pitched rather than like a piece of the bed.
	rainRing = 0.005

	// Rain has far less below this than pink noise does, and rainWash is how loud what is left is
	// against the droplets over it.
	rainBed  = 500
	rainWash = 0.5

	rainSoft, rainHard = 0.55, 1.0
	rainGust           = 7
)

var rainSound = Sound{
	Name: NameRain,
	RMS:  0.45993,
	New: func(g *Gen) Fill {
		var (
			bed  pink
			low  lowpass
			gust = newWalk(rainSoft, rainHard, rainGust, g.Rate)
			near = newBursts(48)
			cut  = onePole(rainBed, g.Rate)
		)

		return func(dst []float32) {
			for i := range dst {
				p := bed.next(g)
				out := (p - low.run(p, cut)) * gust.next(g) * rainWash

				if g.chance(rainFall) {
					// Squared over a range that does not reach zero: uniform amplitudes sound mechanical,
					// every drop the size of the last, but a droplet nobody can hear may as well not exist.
					amp := g.between(0.45, 1)
					near.add(newBurst(
						g.between(rainLow, rainHigh),
						rainRing,
						g.between(rainQuick, rainSlow),
						amp*amp,
						g.Rate,
					))
				}
				dst[i] = out + near.sum(g)
			}
		}
	},
}
