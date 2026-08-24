package aec

import (
	"math"
	"testing"
)

// The denoiser was deafened permanently by one frame of input its arithmetic could not represent, and
// the fault was invisible because the state that carried it is never printed. This drives the filter
// into the same corners on purpose: silence, full scale, DC, and a reference that does not match the
// microphone at all.
//
// Duration is not the point — the regimes are. A leaky integrator reaches denormals in minutes of
// audio, which is a second of CPU here.
func TestStateStaysFiniteUnderAdversarialInput(t *testing.T) {
	const (
		taps   = 256
		frames = 10000 // 64 s of audio per pattern
		hop    = 320
	)

	patterns := []struct {
		name     string
		mic, ref func(i int) int16
	}{
		{"silence", func(int) int16 { return 0 }, func(int) int16 { return 0 }},
		{"loud mic, silent reference", func(i int) int16 { return int16(30000 * math.Sin(float64(i)/8)) }, func(int) int16 { return 0 }},
		{"silent mic, loud reference", func(int) int16 { return 0 }, func(i int) int16 { return int16(30000 * math.Sin(float64(i)/8)) }},
		{"full scale square", func(i int) int16 { return sq(i) }, func(i int) int16 { return sq(i) }},
		{"dc at full scale", func(int) int16 { return 32767 }, func(int) int16 { return 32767 }},
		{"alternating extremes", func(i int) int16 { return alt(i) }, func(i int) int16 { return alt(i) }},
		{"one lsb", func(int) int16 { return 1 }, func(int) int16 { return 1 }},
		{"loud reference, one lsb mic", func(int) int16 { return 1 }, func(i int) int16 { return sq(i) }},
		{"reference uncorrelated with mic", func(i int) int16 { return int16(20000 * math.Sin(float64(i)/3)) }, func(i int) int16 { return int16(20000 * math.Sin(float64(i)/97)) }},
	}

	for _, p := range patterns {
		c, err := New(Config{Taps: taps, Mu: 0.3})
		if err != nil {
			t.Fatal(err)
		}

		mic := make([]int16, hop)
		ref := make([]int16, hop)
		n := 0
		for k := 0; k < frames; k++ {
			for i := range mic {
				mic[i], ref[i] = p.mic(n+i), p.ref(n+i)
			}
			n += hop
			if _, err := c.Process(mic, ref); err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
		}

		bad := 0
		for _, v := range c.w {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				bad++
			}
		}
		for what, v := range map[string]float64{"pow": float64(c.pow), "sumD": c.sumD, "sumE": c.sumE} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Errorf("%s: %s is %v", p.name, what, v)
			}
		}
		if bad > 0 {
			t.Errorf("%s: %d of %d filter taps are non-finite", p.name, bad, len(c.w))
		}

		// Whatever it learned, speech afterwards has to survive it. This is the check that would have
		// caught the denoiser: state can be finite and the output still be silence.
		var loudest int16
		for k := 0; k < 200; k++ {
			for i := range mic {
				mic[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(n+i)/16000))
				ref[i] = 0
			}
			n += hop
			out, err := c.Process(mic, ref)
			if err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
			for _, v := range out {
				if v > loudest {
					loudest = v
				}
			}
		}
		if loudest == 0 {
			t.Errorf("%s: a full-scale tone afterwards comes out as pure zeros", p.name)
		}
		t.Logf("%-34s erle=%6.2f dB  tone after=%5d", p.name, c.ERLE(), loudest)
	}
}

func sq(i int) int16 {
	if i/16%2 == 0 {
		return 32767
	}
	return -32768
}

func alt(i int) int16 {
	if i%2 == 0 {
		return 32767
	}
	return -32768
}
