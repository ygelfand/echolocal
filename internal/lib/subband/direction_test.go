package subband

import (
	"math"
	"math/cmplx"
	"math/rand/v2"
	"testing"
)

// A 36 mm ring of six microphones 60 degrees apart from 108 degrees, and a seventh in the middle.
const (
	ringRadius   = 0.036
	firstMic     = 108 * math.Pi / 180
	micSpacing   = 60 * math.Pi / 180
	speedOfSound = 343.0
	centerMic    = 6

	arrayMics = 7
)

func planeWave(bearing float64, samples int) [][]int16 {
	const tones = 24

	freq := make([]float64, tones)
	phase := make([]float64, tones)
	for i := range freq {
		freq[i] = 300 + 200*float64(i)
		phase[i] = rand.Float64() * 2 * math.Pi
	}

	out := make([][]int16, arrayMics)
	for m := range out {
		out[m] = make([]int16, samples)

		delay := ringRadius / speedOfSound
		if m != centerMic {
			delay *= 1 - math.Cos(bearing-(firstMic+float64(m)*micSpacing))
		}

		for i := range out[m] {
			t := float64(i)/16000 - delay
			var v float64
			for k := range freq {
				v += math.Sin(2*math.Pi*freq[k]*t + phase[k])
			}
			out[m][i] = int16(1000 * v / tones * 8)
		}
	}
	return out
}

func TestVendorBeamsPointSomewhere(t *testing.T) {
	w := vendorWeights(t)

	t.Log("bearing   beam energies (dB relative to the quietest)      loudest")
	pointing := make([]int, 0, 12)

	for step := range 12 {
		bearing := float64(step) * 30 * math.Pi / 180
		frame := planeWave(bearing, 16*testFrame)

		b := w.New()
		b.Mix(frame)

		least := math.MaxFloat64
		for _, e := range b.energy {
			least = min(least, float64(e))
		}

		loudest, line := 0, ""
		for j, e := range b.energy {
			if e > b.energy[loudest] {
				loudest = j
			}
			line += formatDB(float64(e) / least)
		}
		pointing = append(pointing, loudest)
		t.Logf("%4.0f deg  %s   beam %d", float64(step)*30, line, loudest)
	}

	seen := map[int]bool{}
	for _, j := range pointing {
		seen[j] = true
	}
	if len(seen) != w.Beams() {
		t.Errorf("%d of %d beams ever won; the weights are not steering", len(seen), w.Beams())
	}

}

func ringDelay(channel int, bearing float64) float64 {
	late := ringRadius / speedOfSound * 16000
	if channel == centerMic {
		return late
	}
	return late * (1 - math.Cos(bearing-(firstMic+float64(channel)*micSpacing)))
}

func steered(g geometry, toward []float64) *Weights {
	w := &Weights{
		g:      g,
		window: testWindow(g),
		fbf:    make([]complex64, g.Bands*g.Beams*g.Taps*g.Inputs()),
	}
	for band := range g.Bands {
		turn := 2 * math.Pi * float64(band) / float64(g.FFTLen)
		for beam, bearing := range toward {
			for m, ch := range g.mics {
				w.weight(band, beam, 0)[m] = complex64(cmplx.Rect(1, turn*ringDelay(ch, bearing)))
			}
		}
	}
	return w
}

func apart(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 2*math.Pi)
	return min(d, 2*math.Pi-d)
}

func TestBearingsFindWhereEachBeamPoints(t *testing.T) {
	eachGeometry(t, func(t *testing.T, g geometry) {
		if g.Inputs() < 3 {
			t.Skip("a pair of mics can't tell front from back")
		}
		toward := make([]float64, g.Beams)
		for j := range toward {
			toward[j] = math.Mod(0.4+float64(j)*2*math.Pi/float64(g.Beams), 2*math.Pi)
		}
		got := steered(g, toward).Bearings(ringDelay)
		for j := range toward {
			if d := apart(got[j], toward[j]); d > 2*math.Pi/bearingSteps {
				t.Errorf("beam %d points at %.0f deg, want %.0f deg",
					j, got[j]*180/math.Pi, toward[j]*180/math.Pi)
			}
		}
	})
}

func TestLookSteersLikeMix(t *testing.T) {
	eachGeometry(t, func(t *testing.T, g geometry) {
		toward := make([]float64, g.Beams)
		for j := range toward {
			toward[j] = float64(j) * 2 * math.Pi / float64(g.Beams)
		}
		w := steered(g, toward)
		mixing, looking := w.New(), w.New()
		for step := range 6 {
			sound := planeWave(float64(step)*2*math.Pi/6, 16*testFrame)
			mixing.Mix(sound)
			looking.Look(sound)
			if mixing.Beam() != looking.Beam() {
				t.Fatalf("step %d: Look chose beam %d, Mix chose beam %d",
					step, looking.Beam(), mixing.Beam())
			}
		}
	})
}

func TestVendorBearingsMatchTheLoudestBeam(t *testing.T) {
	w := vendorWeights(t)
	bearings := w.Bearings(ringDelay)
	for step := range 12 {
		bearing := float64(step) * 30 * math.Pi / 180
		b := w.New()
		b.Mix(planeWave(bearing, 16*testFrame))
		loudest := 0
		for j, e := range b.energy {
			if e > b.energy[loudest] {
				loudest = j
			}
		}
		if d := apart(bearings[loudest], bearing); d > 2*math.Pi/float64(w.Beams()) {
			t.Errorf("sound from %.0f deg: beam %d won, but it points at %.0f deg",
				bearing*180/math.Pi, loudest, bearings[loudest]*180/math.Pi)
		}
	}
}

func formatDB(ratio float64) string {
	db := 10 * math.Log10(ratio)
	switch {
	case db >= 10:
		return "  +++"
	case db >= 3:
		return "   ++"
	case db >= 1:
		return "    +"
	}
	return "    ."
}
