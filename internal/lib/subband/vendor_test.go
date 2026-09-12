package subband

import (
	"math"
	"os"
	"testing"

	"github.com/ygelfand/echolocal/internal/lib/fft"
)

// The vendor's coefficients are not ours to ship, so the tests that use them run against a copy
// pulled off a device. Which set is there decides which front end gets exercised:
//
//	adb pull /vendor/etc/audio-algorithms /tmp/coefs
//	ECHOLOCAL_VENDOR_DIR=/tmp/coefs go test ./internal/lib/subband/ -run Vendor -v
func vendorWeights(t *testing.T) *Weights {
	t.Helper()
	dir := os.Getenv("ECHOLOCAL_VENDOR_DIR")
	if dir == "" {
		t.Skip("set ECHOLOCAL_VENDOR_DIR to a copy of /vendor/etc/audio-algorithms")
	}

	w, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Logf("loaded %s: %d bands, %d beams, %d of the array's microphones",
		w.Name(), w.Bands(), w.Beams(), w.Inputs())
	return w
}

// Whether the prototype reconstructs at this hop is how we know the fold matches the one it was
// designed for. A window folded the wrong way still runs and still sounds like something, so this is
// the check that says our bank is their bank.
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

	if spread := (most - least) / most; spread > 0.02 {
		t.Errorf("reconstruction gain varies by %.1f%% across the hop, so the fold is wrong", 100*spread)
	}

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
		t.Errorf("reconstruction is off by %.4f, which is not a filter bank in agreement with itself", worst)
	}
}
