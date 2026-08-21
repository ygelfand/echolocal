package tflite

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// referenceSignal is the deterministic input the golden fixture was produced from: two tones and
// a slow amplitude sweep, at the int16 magnitudes openWakeWord feeds the mel model.
func referenceSignal(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		t := float64(i) / 16000
		env := 0.4 + 0.6*math.Sin(2*math.Pi*3*t)
		v := env * (6000*math.Sin(2*math.Pi*440*t) + 2500*math.Sin(2*math.Pi*1750*t))
		out[i] = float32(int16(v))
	}
	return out
}

func readGolden(t *testing.T, path string) []float32 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()

	var out []float32
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := strconv.ParseFloat(line, 32)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, float32(v))
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMelMatchesReferenceRuntime compares our interpreter against TensorFlow Lite's own C runtime
// on the same model file and the same input. The fixture was produced by the reference runtime, so
// any disagreement here is a bug in this package rather than a question of calibration.
func TestMelMatchesReferenceRuntime(t *testing.T) {
	want := readGolden(t, "testdata/mel_reference.txt")

	raw, err := os.ReadFile("../oww/assets/melspectrogram.tflite")
	if err != nil {
		t.Skip(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	in, err := New(m)
	if err != nil {
		t.Fatal(err)
	}

	const samples = 1760
	in.ResizeInput(0, []int{1, samples})
	copy(in.Input(0).F32, referenceSignal(samples))
	if err := in.Invoke(); err != nil {
		t.Fatal(err)
	}

	got := in.Output(0).F32
	if len(got) != len(want) {
		t.Fatalf("got %d values, reference has %d", len(got), len(want))
	}

	var worst float64
	var at int
	for i := range want {
		if d := math.Abs(float64(got[i] - want[i])); d > worst {
			worst, at = d, i
		}
	}
	t.Logf("largest difference %.6g at index %d (ours %.6f, reference %.6f)", worst, at, got[at], want[at])

	// The reference runs the same graph in float32, so only accumulation order should differ.
	if worst > 0.01 {
		t.Errorf("our mel output differs from the reference by %.6g", worst)
	}
}

// referenceMelFrames is the deterministic stand-in for a window of transformed mel frames that the
// embedding fixture was produced from.
func referenceMelFrames(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		f := float64(i)
		out[i] = float32(6 + 4*math.Sin(f*0.037) + 3*math.Cos(f*0.011) - 2*math.Sin(f*0.9))
	}
	return out
}

func TestEmbeddingMatchesReferenceRuntime(t *testing.T) {
	want := readGolden(t, "testdata/embedding_reference.txt")

	raw, err := os.ReadFile("../oww/assets/embedding_model.tflite")
	if err != nil {
		t.Skip(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	in, err := New(m)
	if err != nil {
		t.Fatal(err)
	}

	in.ResizeInput(0, []int{1, 76, 32, 1})
	copy(in.Input(0).F32, referenceMelFrames(76*32))
	if err := in.Invoke(); err != nil {
		t.Fatal(err)
	}

	got := in.Output(0).F32
	if len(got) != len(want) {
		t.Fatalf("got %d values, reference has %d", len(got), len(want))
	}

	var worst float64
	var at int
	for i := range want {
		if d := math.Abs(float64(got[i] - want[i])); d > worst {
			worst, at = d, i
		}
	}
	t.Logf("largest difference %.6g at index %d (ours %.6f, reference %.6f)", worst, at, got[at], want[at])
	if worst > 0.01 {
		t.Errorf("our embedding output differs from the reference by %.6g", worst)
	}
}

// referenceEmbeddings is the deterministic window of embeddings the classifier fixture was produced
// from: 16 frames of 96, the shape an openWakeWord classifier takes.
func referenceEmbeddings() []float32 {
	out := make([]float32, 16*96)
	for i := range out {
		f := float64(i)
		out[i] = float32(0.6*math.Sin(f*0.037) + 0.4*math.Cos(f*0.011) - 0.3*math.Sin(f*0.9))
	}
	return out
}

// TestClassifierMatchesReferenceRuntime checks the shape ops a wake word classifier needs against
// TensorFlow Lite's own runtime. CLASSIFIER names the model the fixture was produced from.
func TestClassifierMatchesReferenceRuntime(t *testing.T) {
	model := os.Getenv("CLASSIFIER")
	if model == "" {
		t.Skip("set CLASSIFIER to a model testdata has a fixture for")
	}

	// The fixture is named after the model, so pointing this at one it was not produced from skips
	// rather than comparing a score against another wake word's.
	stem := strings.TrimSuffix(filepath.Base(model), ".tflite")
	want := readGolden(t, filepath.Join("testdata", "classifier_"+stem+"_reference.txt"))

	raw, err := os.ReadFile(model)
	if err != nil {
		t.Skip(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	in, err := New(m)
	if err != nil {
		t.Fatal(err)
	}

	copy(in.Input(0).F32, referenceEmbeddings())
	if err := in.Invoke(); err != nil {
		t.Fatal(err)
	}

	got := in.Output(0).F32
	if len(got) != len(want) {
		t.Fatalf("got %d values, reference has %d", len(got), len(want))
	}

	var worst float64
	for i := range want {
		if d := math.Abs(float64(got[i] - want[i])); d > worst {
			worst = d
		}
	}
	t.Logf("ours %v, reference %v, largest difference %.6g", got, want, worst)

	if worst > 1e-4 {
		t.Errorf("our classifier output differs from the reference by %.6g", worst)
	}
}
