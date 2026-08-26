package denoise

import (
	"math"
	"math/rand"
	"testing"
)

// The exact-zero path is guarded. This asks the same question of audio that is merely very quiet,
// which is what a room gives a device whose gain is low: does the estimator survive it, and does
// speech afterwards still come out?
func TestVeryQuietAudioDoesNotDeafenTheFilter(t *testing.T) {
	for _, lsb := range []float64{0.4, 1, 2, 8, 32} {
		f := New(16000)
		in := make([]float64, f.Frame())
		out := make([]float64, f.Hop())
		rng := rand.New(rand.NewSource(1))

		// Twenty minutes of a near-silent room.
		for k := 0; k < 60000; k++ {
			for i := range in {
				in[i] = math.Round(lsb * rng.NormFloat64())
			}
			f.Push(in, out)
		}

		bad := 0
		for _, v := range append(append([]float64{}, f.noise...), f.clean...) {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				bad++
			}
		}

		var loudest int16
		for k := 0; k < 400; k++ {
			for i := range in {
				in[i] = 8000 * math.Sin(2*math.Pi*440*float64(k*f.Hop()+i)/16000)
			}
			f.Push(in, out)
			for _, v := range out {
				if s := clampSample(v); s > loudest {
					loudest = s
				}
			}
		}

		t.Logf("room at %5.1f lsb rms: %d non-finite, speech afterwards peaks at %6d", lsb, bad, loudest)
		if loudest == 0 {
			t.Errorf("room at %.1f lsb deafened the filter", lsb)
		}
	}
}
