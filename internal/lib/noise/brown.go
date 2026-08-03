package noise

const NameBrown = "Brown"

var brownNoise = Sound{
	Name: NameBrown,
	RMS:  0.05677,
	New: func(g *Gen) Fill {
		var f brown
		return func(dst []float32) {
			for i := range dst {
				dst[i] = f.next(g)
			}
		}
	},
}

// brown integrates white. The leak is what stops it walking off to a rail and staying there, and it
// puts a corner at about 150 Hz, below which brown flattens rather than rising forever.
type brown struct{ sum float32 }

func (b *brown) next(g *Gen) float32 {
	b.sum = (b.sum + 0.02*g.white()) / 1.02
	return b.sum
}
