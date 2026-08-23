package denoise

import (
	"math"
	"testing"
)

// Values from the series at 220 decimal digits. The range is what the gain rule asks for, since the
// posterior SNR is capped at 40.
func TestExponentialIntegral(t *testing.T) {
	for _, c := range []struct{ x, want float64 }{
		{0.001, 6.331539364136149},
		{0.01, 4.0379295765381142},
		{0.1, 1.8229239584193906},
		{0.5, 0.55977359477616084},
		{1, 0.21938393439552029},
		{1.5, 0.10001958240663265},
		{2, 0.048900510708061118},
		{5, 0.0011482955912753257},
		{10, 4.1569689296853246e-06},
		{20, 9.8355252906498815e-11},
		{40, 1.036773261451657e-19},
	} {
		got := e1(c.x)
		if rel := math.Abs(got-c.want) / c.want; rel > 1e-13 {
			t.Errorf("e1(%g) = %.17g, want %.17g (relative %.2g)", c.x, got, c.want, rel)
		}
	}
}

func TestExponentialIntegralEdges(t *testing.T) {
	if got := e1(0); !math.IsInf(got, 1) {
		t.Errorf("e1(0) = %v, want +Inf", got)
	}
	if got := e1(-1); !math.IsInf(got, 1) {
		t.Errorf("e1(-1) = %v, want +Inf", got)
	}
	if got := e1(1000); got != 0 {
		t.Errorf("e1(1000) = %v, want 0", got)
	}
}

// E1 falls monotonically, which the two branches have to agree on across the join at x = 1.
func TestExponentialIntegralIsSmoothAcrossTheJoin(t *testing.T) {
	prev := math.Inf(1)
	for x := 0.9; x <= 1.1; x += 0.005 {
		got := e1(x)
		if got >= prev {
			t.Fatalf("e1(%g) = %v did not fall below %v", x, got, prev)
		}
		prev = got
	}

	below, above := e1(1-1e-9), e1(1+1e-9)
	if math.Abs(below-above) > 1e-8 {
		t.Errorf("the branches disagree at x = 1: %.17g against %.17g", below, above)
	}
}
