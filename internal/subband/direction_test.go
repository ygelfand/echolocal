package subband

import (
	"math"
	"math/rand/v2"
	"testing"
)

// The array the weights were generated for, as measured with directional taps: a 36 mm ring of six
// microphones 60 degrees apart with the first at 108 degrees, and a seventh in the middle. Only the
// synthetic source below needs it — where a beam ends up pointing is not asserted.
const (
	ringRadius   = 0.036
	firstMic     = 108 * math.Pi / 180
	micSpacing   = 60 * math.Pi / 180
	speedOfSound = 343.0
	centerMic    = 6
)

// planeWave is what the array hears from a source far enough away that the wavefront is flat: the
// same signal at each microphone, arriving at a time that depends on where the microphone sits.
//
// The signal is a sum of tones so the delays can be exact. Interpolating a delay would soften the
// top of the band, which is the part that carries the direction.
func planeWave(bearing float64, samples int) [][]int16 {
	const tones = 24

	freq := make([]float64, tones)
	phase := make([]float64, tones)
	for i := range freq {
		freq[i] = 300 + 200*float64(i)
		phase[i] = rand.Float64() * 2 * math.Pi
	}

	out := make([][]int16, Inputs)
	for m := range out {
		out[m] = make([]int16, samples)

		// Seconds of arrival, offset so none is negative. A microphone facing the source hears first.
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

// Whether the weights are being read correctly comes down to one question: does a source in front of
// a beam come out of that beam louder than out of the others? If the taps or the conjugation are
// wrong the weights still sum to something, but nothing points anywhere and every beam answers the
// same to every direction.
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
	if len(seen) != Beams {
		t.Errorf("%d of %d beams ever won; the weights are not steering", len(seen), Beams)
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
