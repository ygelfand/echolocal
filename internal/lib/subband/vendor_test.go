package subband

import (
	"math"
	"os"
	"testing"

	"github.com/ygelfand/libcountertop/pkg/audio/fft"
)

// The vendor coefficients are not ours to ship. ECHOLOCAL_VENDOR_TUNING names the tuning to load
// from them, for a board other than the dot.
func vendorWeights(t *testing.T) *Weights {
	t.Helper()
	dir := os.Getenv("ECHOLOCAL_VENDOR_DIR")
	if dir == "" {
		t.Skip("set ECHOLOCAL_VENDOR_DIR to a copy of /vendor/etc/audio-algorithms")
	}

	tunings := DotTunings
	if name := os.Getenv("ECHOLOCAL_VENDOR_TUNING"); name != "" {
		tunings = []Tuning{Tuning(name)}
	}

	w, err := Load(dir, tunings)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Logf("loaded %s: %d bands, %d beams, %d of the array's microphones",
		w.Name(), w.Bands(), w.Beams(), w.Inputs())
	return w
}

func TestVendorWindowReconstructs(t *testing.T) {
	w := vendorWeights(t)
	g := w.g

	var least, most float32 = math.MaxFloat32, 0
	folded := make([]float32, g.FFTLen)
	for n, h := range w.window {
		folded[n%g.FFTLen] += h
	}
	for start := range g.Hop {
		var gain float32
		for n := start; n < g.WindowLen; n += g.Hop {
			gain += w.window[n] * folded[n%g.FFTLen]
		}
		least, most = min(least, gain), max(most, gain)
	}
	t.Logf("gain %.6f..%.6f, spread %.2f%%", least, most, 100*(most-least)/most)

	f := fft.New(g.FFTLen)
	a := newAnalysis(g, w.window, f)
	s := newSynthesis(g, w.window, f)
	scale := 1 / bankGain(g, w.window)

	const frames = 60
	in := make([]float32, frames*g.Hop)
	for i := range in {
		tt := float64(i) / 16000
		in[i] = float32(0.4*math.Sin(2*math.Pi*440*tt) + 0.3*math.Sin(2*math.Pi*1900*tt))
	}

	out := make([]float32, len(in))
	bands := make([]complex64, g.Bands)
	for k := 0; k*g.Hop < len(in); k++ {
		a.push(in[k*g.Hop:(k+1)*g.Hop], bands)
		s.pull(bands, out[k*g.Hop:(k+1)*g.Hop])
	}

	delay := g.WindowLen - g.Hop
	var worst float64
	for i := delay + g.Hop; i < len(in); i++ {
		worst = max(worst, math.Abs(float64(out[i]*scale-in[i-delay])))
	}
	t.Logf("worst reconstruction error %.5f of a peak of 0.7", worst)

	if worst > 0.02 {
		t.Errorf(
			"reconstruction is off by %.4f, which is not a filter bank in agreement with itself",
			worst,
		)
	}
}
