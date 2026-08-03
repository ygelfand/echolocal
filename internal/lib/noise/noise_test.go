package noise

import (
	"math"
	"testing"
)

// rate is what the speaker runs at, which is where these are used.
const rate = 48000

// The bands the slope is measured over: octaves, high enough that brown's own corner at 150 Hz is well
// below them and low enough to stay under Nyquist.
var bands = []float64{750, 1500, 3000, 6000, 12000}

const (
	blockSize = 4096
	blocks    = 200
)

// TestSlopes is what tells the colours apart, since getting a filter wrong yields something that still
// sounds like noise and nothing else here would notice.
func TestSlopes(t *testing.T) {
	for _, tc := range []struct {
		name string
		want float64
	}{
		{NameWhite, 0},
		{NamePink, -3},
		{NameBrown, -6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := slope(mixSeeded(rate, 1, tc.name))
			t.Logf("%s: %.2f dB/octave", tc.name, got)

			if math.Abs(got-tc.want) > 0.8 {
				t.Errorf("%s falls %.2f dB an octave, want %.0f", tc.name, got, tc.want)
			}
		})
	}
}

// TestLevelsMatch is why one sound can be swapped for another while it is playing, and it is where the
// RMS and Peak fields come from: the numbers this logs are the ones that go in the files.
func TestLevelsMatch(t *testing.T) {
	for _, name := range Names() {
		s := byName[name]
		rms, peak := levels(mixSeeded(rate, 2, name), blockSize*blocks)

		t.Logf("%-9s rms %.4f (%5.1f dBFS)  peak %.3f  crest %.1f  RMS %.5f  Peak %.5f",
			name, rms, 20*math.Log10(float64(rms)), peak, peak/rms,
			s.RMS*rms/Level, s.Peak*peak/Loudest)

		got, want := rms, float32(Level)
		if s.Peak > 0 {
			got, want = peak, Loudest
		}
		if off := 20 * math.Log10(float64(got/want)); math.Abs(off) > 1 {
			t.Errorf("%s runs %.1f dB off where it should sit", name, off)
		}
		if peak >= 1 {
			t.Errorf("%s peaks at %.3f, which is clipped", name, peak)
		}
	}
}

// A resonator has to pass noise at the same level wherever it is placed. It did not: the gain at
// resonance goes as 1/((1-r)·2·sin ω), so droplets came out louder the lower they were pitched and wind
// changed level as its band swept. Everything built on burst and reson depends on this.
func TestResonatorGainDoesNotDependOnPitch(t *testing.T) {
	g := &Gen{Rate: rate, rand: newRand(1)}

	// Nothing narrower: a filter that rings for a fifth of a second is a couple of hertz wide, and
	// measuring one takes longer than the whole suite.
	for _, decay := range []float32{0.002, 0.02} {
		var low, high float32 = 1, 0

		// From 400 Hz up, which is where the sounds place them. Lower than that a band this wide reaches
		// past zero and there is no meaningful peak left to normalise.
		for _, freq := range []float32{400, 1600, 6400} {
			r := newReson(freq, decay, rate)

			var sum float64
			const n = 2 * rate
			for range n {
				y := r.run(g.white())
				sum += float64(y) * float64(y)
			}

			rms := float32(math.Sqrt(sum / n))
			low, high = min(low, rms), max(high, rms)
		}

		if spread := 20 * math.Log10(float64(high/low)); spread > 1.5 {
			t.Errorf("ringing for %gs, gain ranges over %.1f dB across pitch", decay, spread)
		}
	}
}

// A mix has to come out at the same level as one of them, which is the whole point of mixing at a
// known level: putting crickets under wind must not be a step up in volume.
func TestMixHoldsTheLevel(t *testing.T) {
	rms, peak := levels(mixSeeded(rate, 3, NameWind, NameCrickets), blockSize*blocks)
	t.Logf("wind+crickets: rms %.4f, peak %.3f", rms, peak)

	if off := 20 * math.Log10(float64(rms/Level)); math.Abs(off) > 1.5 {
		t.Errorf("a mix runs %.1f dB off the level one sound plays at", off)
	}
	if peak >= 1 {
		t.Errorf("a mix peaks at %.3f, which is clipped", peak)
	}
}

func TestUnknownSoundsPlayNothing(t *testing.T) {
	if New("Bagpipes", rate) != nil {
		t.Error("played a sound this build does not have")
	}
	if Mix(rate) != nil {
		t.Error("played nothing at all as though it were something")
	}
	if Mix(rate, "Bagpipes", NamePink) == nil {
		t.Error("one unknown name in a mix threw the mix away")
	}
}

// slope fits dB against octave over the bands, which is the number that names a colour.
func slope(fill Fill) float64 {
	power := make([]float64, len(bands))
	buf := make([]float32, blockSize)

	for range blocks {
		fill(buf)
		for i, f := range bands {
			power[i] += goertzel(buf, f)
		}
	}

	// Least squares over octave index: the bands are an octave apart, so the index is the x axis.
	var sx, sy, sxx, sxy float64
	for i, p := range power {
		x, y := float64(i), 10*math.Log10(p/blocks)
		sx, sy, sxx, sxy = sx+x, sy+y, sxx+x*x, sxy+x*y
	}
	n := float64(len(power))
	return (n*sxy - sx*sy) / (n*sxx - sx*sx)
}

// goertzel is the power at one frequency, which is all that is wanted here: five bins rather than a
// whole spectrum.
func goertzel(samples []float32, freq float64) float64 {
	k := math.Round(freq / rate * float64(len(samples)))
	w := 2 * math.Pi * k / float64(len(samples))
	coeff := 2 * math.Cos(w)

	var s1, s2 float64
	for _, v := range samples {
		s := float64(v) + coeff*s1 - s2
		s2, s1 = s1, s
	}
	return s1*s1 + s2*s2 - coeff*s1*s2
}

func levels(fill Fill, n int) (rms, peak float32) {
	buf := make([]float32, blockSize)
	var sum float64

	for range n / blockSize {
		fill(buf)
		for _, v := range buf {
			sum += float64(v) * float64(v)
			peak = max(peak, float32(math.Abs(float64(v))))
		}
	}
	return float32(math.Sqrt(sum / float64(n))), peak
}
