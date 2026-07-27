package speaker

import (
	"math"
	"testing"
)

func TestGainForStep(t *testing.T) {
	cases := []struct {
		out  Output
		step int
		want float32
	}{
		{OutputSpeaker, 0, 0},
		{OutputSpeaker, 30, 1},
		{OutputSpeaker, 15, 0.398}, // -8 dB
		{OutputSpeaker, 5, 0.0708}, // -23 dB
		{OutputHeadphone, 0, 0},
		{OutputHeadphone, 30, 1},
		{OutputHeadphone, 15, 0.112}, // -19 dB
	}

	for _, c := range cases {
		got := gainForStep(c.out, c.step)
		if math.Abs(float64(got-c.want)) > 0.001 {
			t.Errorf("gainForStep(%s, %d) = %v, want %v", c.out, c.step, got, c.want)
		}
	}
}

// Steps outside the range clamp rather than panicking on the curve index.
func TestGainForStepClamps(t *testing.T) {
	if got := gainForStep(OutputSpeaker, -5); got != 0 {
		t.Errorf("gainForStep(speaker, -5) = %v, want 0", got)
	}
	if got := gainForStep(OutputSpeaker, 99); got != 1 {
		t.Errorf("gainForStep(speaker, 99) = %v, want 1", got)
	}
}

// An unknown output falls back to the speaker curve rather than a zero curve, which would be
// silence.
func TestGainForStepUnknownOutput(t *testing.T) {
	if got := gainForStep(Output("bluetooth"), 30); got != 1 {
		t.Errorf("gainForStep(bluetooth, 30) = %v, want 1", got)
	}
}
