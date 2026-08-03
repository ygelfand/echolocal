package noise

const NamePink = "Pink"

var pinkNoise = Sound{
	Name: NamePink,
	RMS:  1.76,
	New: func(g *Gen) Fill {
		var f pink
		return func(dst []float32) {
			for i := range dst {
				dst[i] = f.next(g)
			}
		}
	},
}

// pink is Paul Kellett's filter, which holds to a 3 dB slope within a twentieth of a dB from 9 Hz up.
// The coefficients were derived at 44.1 kHz and tilt a hair at 48, far below anything audible in noise.
type pink struct{ b [7]float32 }

func (p *pink) next(g *Gen) float32 {
	w := g.white()
	p.b[0] = 0.99886*p.b[0] + w*0.0555179
	p.b[1] = 0.99332*p.b[1] + w*0.0750759
	p.b[2] = 0.96900*p.b[2] + w*0.1538520
	p.b[3] = 0.86650*p.b[3] + w*0.3104856
	p.b[4] = 0.55000*p.b[4] + w*0.5329522
	p.b[5] = -0.7616*p.b[5] - w*0.0168980

	out := p.b[0] + p.b[1] + p.b[2] + p.b[3] + p.b[4] + p.b[5] + p.b[6] + w*0.5362
	p.b[6] = w * 0.115926
	return out
}
