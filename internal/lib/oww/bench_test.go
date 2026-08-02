package oww

import (
	"testing"

	"github.com/ygelfand/echolocal/internal/lib/tflite"
)

func BenchmarkMel(b *testing.B) {
	m, _ := tflite.Parse(melModel)
	in, err := tflite.New(m)
	if err != nil {
		b.Fatal(err)
	}
	in.ResizeInput(0, []int{1, melLookback + Step})

	for b.Loop() {
		if err := in.Invoke(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEmbedding(b *testing.B) {
	m, _ := tflite.Parse(embedModel)
	in, err := tflite.New(m)
	if err != nil {
		b.Fatal(err)
	}
	in.ResizeInput(0, []int{1, embedFrames, melBins, 1})

	for b.Loop() {
		if err := in.Invoke(); err != nil {
			b.Fatal(err)
		}
	}
}

// One step is 80 ms of audio, so anything approaching 80 ms here is not viable on the device.
func BenchmarkStep(b *testing.B) {
	e, err := New()
	if err != nil {
		b.Fatal(err)
	}
	audio := make([]int16, Step)

	for b.Loop() {
		if _, err := e.Process(audio); err != nil {
			b.Fatal(err)
		}
	}
}
