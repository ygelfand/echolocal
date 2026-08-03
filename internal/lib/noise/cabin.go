package noise

const NameCabin = "Cabin"

const (
	cabinCut  = 500.0
	cabinRing = 0.25
	cabinBody = 0.5
)

// Spaced unevenly: modes an octave apart would read as a chord.
var cabinModes = []float32{88, 132, 197}

var cabinSound = Sound{
	Name: NameCabin,
	RMS:  0.051478,
	New: func(g *Gen) Fill {
		var (
			bed   brown
			air   lowpass
			cut   = onePole(cabinCut, g.Rate)
			drift = newWalk(0.92, 1.08, 25, g.Rate)
			body  = make([]reson, len(cabinModes))
		)
		for i, f := range cabinModes {
			body[i] = newReson(f, cabinRing, g.Rate)
		}

		return func(dst []float32) {
			for i := range dst {
				out := air.run(bed.next(g), cut) * drift.next(g)

				w := g.white()
				for j := range body {
					out += cabinBody * body[j].run(w)
				}
				dst[i] = out
			}
		}
	},
}
