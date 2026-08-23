package subband

import (
	"math"
	"os"
	"testing"

	"github.com/ygelfand/echolocal/internal/lib/fft"
)

// The vendor's coefficients are not ours to ship, so the tests that use them run against a copy
// pulled off a device:
//
//	adb pull /vendor/etc/audio-algorithms/coefs_FBF.cfg /tmp/coefs
//	adb pull /vendor/etc/audio-algorithms/coefs_FilterBank_640.cfg /tmp/coefs
//	ECHOLOCAL_VENDOR_DIR=/tmp/coefs go test ./internal/subband/ -run Vendor -v
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
	return w
}

// Whether the prototype reconstructs at this hop is how we know the fold matches the one it was
// designed for. A window folded the wrong way still runs and still sounds like something, so this is
// the check that says our bank is their bank.
func TestVendorWindowReconstructs(t *testing.T) {
	w := vendorWeights(t)

	var least, most float32 = math.MaxFloat32, 0
	var folded [FFTLen]float32
	for n, h := range w.window {
		folded[n%FFTLen] += h
	}
	for start := range Hop {
		var g float32
		for n := start; n < WindowLen; n += Hop {
			g += w.window[n] * folded[n%FFTLen]
		}
		least, most = min(least, g), max(most, g)
	}
	t.Logf("gain %.6f..%.6f, spread %.2f%%", least, most, 100*(most-least)/most)

	if spread := (most - least) / most; spread > 0.02 {
		t.Errorf("reconstruction gain varies by %.1f%% across the hop, so the fold is wrong", 100*spread)
	}

	f := fft.New(FFTLen)
	a := newAnalysis(w.window, f)
	s := newSynthesis(w.window, f)
	scale := 1 / bankGain(w.window)

	const frames = 60
	in := make([]float32, frames*Hop)
	for i := range in {
		tt := float64(i) / 16000
		in[i] = float32(0.4*math.Sin(2*math.Pi*440*tt) + 0.3*math.Sin(2*math.Pi*1900*tt))
	}

	out := make([]float32, len(in))
	bands := make([]complex64, Bands)
	for k := 0; k*Hop < len(in); k++ {
		a.push(in[k*Hop:(k+1)*Hop], bands)
		s.pull(bands, out[k*Hop:(k+1)*Hop])
	}

	const delay = WindowLen - Hop
	var worst float64
	for i := delay + Hop; i < len(in); i++ {
		worst = max(worst, math.Abs(float64(out[i]*scale-in[i-delay])))
	}
	t.Logf("worst reconstruction error %.5f of a peak of 0.7", worst)

	if worst > 0.02 {
		t.Errorf("reconstruction is off by %.4f, which is not a filter bank in agreement with itself", worst)
	}
}
