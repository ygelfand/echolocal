package subband

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/ygelfand/libcountertop/pkg/audio/fft"
)

const testFrame = 320

// The tunings the dot build allows, as internal/hardware/mic/array.go lists them.
var dotTunings = []Tuning{FBF, FBFV2, RadarFBFV2}

func eachGeometry(t *testing.T, f func(*testing.T, geometry)) {
	t.Helper()
	for _, g := range known {
		t.Run(string(g.name), func(t *testing.T) { f(t, g) })
	}
}

func testWindow(g geometry) []float32 {
	w := make([]float32, g.WindowLen)
	for n := range g.FFTLen {
		hann := 0.5 - 0.5*math.Cos(2*math.Pi*float64(n)/float64(g.FFTLen))
		w[n] = float32(math.Sqrt(hann))
	}
	return w
}

func TestBankGainOfAPerfectWindowIsOne(t *testing.T) {
	eachGeometry(t, func(t *testing.T, g geometry) {
		if got := bankGain(g, testWindow(g)); math.Abs(float64(got)-1) > 1e-5 {
			t.Errorf("bankGain = %v, want 1", got)
		}
	})
}

func TestBankReconstructs(t *testing.T) {
	eachGeometry(t, func(t *testing.T, g geometry) {
		const frames = 40

		window := testWindow(g)
		f := fft.New(g.FFTLen)
		a := newAnalysis(g, window, f)
		s := newSynthesis(g, window, f)

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
		for i := delay; i < len(in); i++ {
			if diff := math.Abs(float64(out[i] - in[i-delay])); diff > 1e-3 {
				t.Fatalf("sample %d = %v, want %v", i, out[i], in[i-delay])
			}
		}
	})
}

func alone(beams int) [][]int {
	out := make([][]int, beams)
	for j := range out {
		out[j] = []int{j}
	}
	return out
}

func passThrough(g geometry) *Weights {
	w := &Weights{
		g:      g,
		window: testWindow(g),
		fbf:    make([]complex64, g.Bands*g.Beams*g.Taps*g.Inputs()),
	}
	for band := range g.Bands {
		for beam := range g.Beams {
			w.weight(band, beam, 0)[beam%g.Inputs()] = 1
		}
	}
	return w
}

func TestBeamFollowsTheTalker(t *testing.T) {
	eachGeometry(t, func(t *testing.T, g geometry) {
		const (
			talker = 3
			calls  = 140
		)
		b := passThrough(g).New()

		frame := make([][]int16, g.Channels())
		for m := range frame {
			frame[m] = make([]int16, testFrame)
		}

		got := 0
		for call := range calls {
			speaking := call/15%2 == 0
			for i := range testFrame {
				for m := range frame {
					frame[m][i] = int16(rand.N(200) - 100)
				}
				if speaking {
					tt := float64(call*testFrame+i) / 16000
					frame[g.mics[talker]][i] += int16(8000 * math.Sin(2*math.Pi*500*tt))
				}
			}
			got += len(b.Mix(frame))
		}

		if want := calls*testFrame - g.Hop; got < want {
			t.Errorf("Mix returned %d samples of %d, want at least %d", got, calls*testFrame, want)
		}
		group := g.groups[b.using]
		if !slices.ContainsFunc(group, func(beam int) bool { return beam%g.Inputs() == talker }) {
			t.Errorf(
				"listening to beams %v, none of them microphone %d where the talker is",
				group,
				talker,
			)
		}
	})
}

func TestOddFrameSizesKeepEverySample(t *testing.T) {
	eachGeometry(t, func(t *testing.T, g geometry) {
		b := passThrough(g).New()

		frame := make([][]int16, g.Channels())
		for m := range frame {
			frame[m] = make([]int16, 100)
		}

		total := 0
		for range 10 {
			total += len(b.Mix(frame))
		}
		if want := 10 * 100 / g.Hop * g.Hop; total < want {
			t.Errorf("returned %d samples of 1000, want at least %d", total, want)
		}
	})
}

func TestLoadWithoutCoefficients(t *testing.T) {
	if _, err := Load(t.TempDir(), dotTunings...); err == nil {
		t.Fatal("an empty directory loaded")
	}
}

func TestLoadRefusesABoardThatAllowsNoTuning(t *testing.T) {
	if _, err := Load(radarDir(t, make([]float32, radarWeightCount()))); err == nil {
		t.Fatal("Load with no tunings allowed loaded a coefficient set")
	}
}
