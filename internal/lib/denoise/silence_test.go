package denoise

import (
	"math"
	"testing"
)

// A bin with no energy at all, once a noise estimate exists, drives the gain to +Inf and the estimate
// to Inf*0. That is NaN, it is kept in clean, and clean feeds the next frame's prior — so one frame of
// digital silence deafens the filter for good. The hardware mute and a suspended codec both produce
// exactly that frame.
func TestDigitalSilenceDoesNotDeafenTheFilter(t *testing.T) {
	f := New(16000)
	in := make([]float64, f.Frame())
	out := make([]float64, f.Hop())

	tone := func(k int) {
		for i := range in {
			in[i] = 8000 * math.Sin(2*math.Pi*440*float64(k*f.Hop()+i)/16000)
		}
	}

	for k := range 40 {
		tone(k)
		f.Push(in, out)
	}

	clear(in)
	f.Push(in, out)

	var poisoned int
	for _, v := range f.clean {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			poisoned++
		}
	}
	t.Logf("after one silent frame, %d of %d bins hold a non-finite estimate", poisoned, len(f.clean))

	var loudest int16
	for k := range 40 {
		tone(k)
		f.Push(in, out)
		for _, v := range out {
			if s := clampSample(v); s > loudest {
				loudest = s
			}
		}
	}
	if loudest == 0 {
		t.Fatalf("a full-scale tone now comes out as pure zeros: the filter is permanently deaf")
	}
	t.Logf("tone recovered at peak %d", loudest)
}
