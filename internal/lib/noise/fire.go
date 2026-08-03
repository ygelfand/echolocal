package noise

const NameFire = "Fire"

// How many crackles arrive a second is itself wandering, so it comes in flurries and then almost stops.
const (
	fireRumble = 220.0

	fireQuiet, fireFlare = 8.0, 90.0
	fireFlurry           = 2.5

	fireLow, fireHigh   = 1200, 6000
	fireRing            = 0.0008
	fireQuick, fireSlow = 0.001, 0.005
)

var fireSound = Sound{
	Name: NameFire,
	RMS:  0.04644,
	New: func(g *Gen) Fill {
		var (
			bed     brown
			hearth  lowpass
			cut     = onePole(fireRumble, g.Rate)
			flurry  = newWalk(fireQuiet, fireFlare, fireFlurry, g.Rate)
			crackle = newBursts(32)
		)

		return func(dst []float32) {
			for i := range dst {
				out := hearth.run(bed.next(g), cut)

				if g.chance(flurry.next(g)) {
					amp := g.between(0, 1)
					crackle.add(newBurst(
						g.between(fireLow, fireHigh),
						fireRing,
						g.between(fireQuick, fireSlow),
						amp*amp*amp,
						g.Rate,
					))
				}
				dst[i] = out + crackle.sum(g)
			}
		}
	},
}
