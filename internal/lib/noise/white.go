package noise

const NameWhite = "White"

var whiteNoise = Sound{
	Name: NameWhite,
	RMS:  0.5777,
	New: func(g *Gen) Fill {
		return func(dst []float32) {
			for i := range dst {
				dst[i] = g.white()
			}
		}
	},
}
