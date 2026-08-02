package tflite

import (
	"math"
	"os"
	"testing"
)

func embeddingModel(t *testing.T) *Model {
	t.Helper()
	raw, err := os.ReadFile("../oww/assets/embedding_model.tflite")
	if err != nil {
		t.Skip(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The streaming path must agree with the windowed model exactly: same filters, same accumulation
// order, only fewer rows recomputed. Anything else means the graph is not what NewStream checked
// it for.
func TestStreamMatchesWindowedModel(t *testing.T) {
	m := embeddingModel(t)

	const (
		window = 76
		bins   = 32
		stride = 8
		steps  = 6
	)

	// A deterministic ramp with enough variation that a wrong row alignment cannot pass.
	total := window + steps*stride
	mels := make([]float32, total*bins)
	for i := range mels {
		mels[i] = float32(math.Sin(float64(i)*0.07)) + float32(i%bins)/64
	}

	windowed, err := New(m)
	if err != nil {
		t.Fatal(err)
	}

	s, err := NewStream(m, []int{1, window, bins, 1})
	if err != nil {
		t.Fatal(err)
	}
	if s.Warmup() != window {
		t.Errorf("warmup = %d, want %d", s.Warmup(), window)
	}

	// Prime with the first window, then compare each subsequent step.
	got, err := s.Write(mels[:window*bins])
	if err != nil {
		t.Fatal(err)
	}
	compareToWindow(t, windowed, mels, 0, window, bins, got)

	for step := 1; step <= steps; step++ {
		from := window + (step-1)*stride
		got, err := s.Write(mels[from*bins : (from+stride)*bins])
		if err != nil {
			t.Fatal(err)
		}
		compareToWindow(t, windowed, mels, step*stride, window, bins, got)
	}
}

func compareToWindow(t *testing.T, in *Interpreter, mels []float32, offset, window, bins int, got []float32) {
	t.Helper()

	in.ResizeInput(0, []int{1, window, bins, 1})
	copy(in.Input(0).F32, mels[offset*bins:(offset+window)*bins])
	if err := in.Invoke(); err != nil {
		t.Fatal(err)
	}
	want := in.Output(0).F32

	if len(got) != len(want) {
		t.Fatalf("offset %d: got %d values, want %d", offset, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("offset %d: value %d is %v streaming, %v windowed", offset, i, got[i], want[i])
		}
	}
}

func BenchmarkStreamStep(b *testing.B) {
	raw, err := os.ReadFile("../oww/assets/embedding_model.tflite")
	if err != nil {
		b.Skip(err)
	}
	m, err := Parse(raw)
	if err != nil {
		b.Fatal(err)
	}
	s, err := NewStream(m, []int{1, 76, 32, 1})
	if err != nil {
		b.Fatal(err)
	}

	prime := make([]float32, 76*32)
	for i := range prime {
		prime[i] = float32(i%32) / 32
	}
	if _, err := s.Write(prime); err != nil {
		b.Fatal(err)
	}
	step := make([]float32, 8*32)

	for b.Loop() {
		if _, err := s.Write(step); err != nil {
			b.Fatal(err)
		}
	}
}
