package oww

import (
	"math"
	"testing"

	"github.com/ygelfand/echolocal/internal/tflite"
)

func TestMelModelShapes(t *testing.T) {
	m, err := tflite.Parse(melModel)
	if err != nil {
		t.Fatal(err)
	}
	in, err := tflite.New(m)
	if err != nil {
		t.Fatal(err)
	}

	in.ResizeInput(0, []int{1, melLookback + Step})
	if err := in.Invoke(); err != nil {
		t.Fatal(err)
	}

	out := in.Output(0)
	frames := out.Count() / melBins
	t.Logf("mel: %d samples in, shape %v out, %d frames", melLookback+Step, out.Shape, frames)
	if frames != embedStride {
		t.Errorf("got %d frames per step, want %d", frames, embedStride)
	}
}

func TestEmbeddingModelShapes(t *testing.T) {
	m, err := tflite.Parse(embedModel)
	if err != nil {
		t.Fatal(err)
	}
	in, err := tflite.New(m)
	if err != nil {
		t.Fatal(err)
	}

	in.ResizeInput(0, []int{1, embedFrames, melBins, 1})
	for i := range in.Input(0).F32 {
		in.Input(0).F32[i] = 1
	}
	if err := in.Invoke(); err != nil {
		t.Fatal(err)
	}

	out := in.Output(0)
	t.Logf("embedding: shape %v out, %d values", out.Shape, out.Count())
	if out.Count() != embedDims {
		t.Errorf("got %d values, want %d", out.Count(), embedDims)
	}
	for i, v := range out.F32 {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("embedding value %d is %v", i, v)
		}
	}
}

// Silence must run the whole pipeline without producing a detection for a loaded model, which is
// also the cheapest check that every kernel the models need is wired up.
func TestProcessSilence(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}

	silence := make([]int16, Step)
	for range 20 {
		if _, err := e.Process(silence); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(e.feats) / embedDims; got == 0 {
		t.Error("no embeddings were produced")
	}
}
