package subband

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/ygelfand/echolocal/internal/lib/fft"
)

// testFrame is the frame the microphone hands out, 20 ms at 16 kHz.
const testFrame = 320

// testWindow is a prototype the bank reconstructs exactly: the root of a periodic Hann over one
// transform, zero beyond it. Its squares sum to one at this hop, and having no energy past FFTLen
// means no aliasing between folds, so any reconstruction error the test sees is the bank's own.
func testWindow() []float32 {
	w := make([]float32, WindowLen)
	for n := range FFTLen {
		hann := 0.5 - 0.5*math.Cos(2*math.Pi*float64(n)/float64(FFTLen))
		w[n] = float32(math.Sqrt(hann))
	}
	return w
}

func TestBankGainOfAPerfectWindowIsOne(t *testing.T) {
	if g := bankGain(testWindow()); math.Abs(float64(g)-1) > 1e-5 {
		t.Errorf("bankGain = %v, want 1", g)
	}
}

func TestBankReconstructs(t *testing.T) {
	const frames = 40

	window := testWindow()
	f := fft.New(FFTLen)
	a := newAnalysis(window, f)
	s := newSynthesis(window, f)

	// Tones well below the band the bank drops, so what comes back should be what went in.
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

	// The bank delays by everything but the newest frame.
	const delay = WindowLen - Hop
	for i := delay; i < len(in); i++ {
		if diff := math.Abs(float64(out[i] - in[i-delay])); diff > 1e-3 {
			t.Fatalf("sample %d = %v, want %v", i, out[i], in[i-delay])
		}
	}
}

// passThrough is one weight set per beam that takes a single microphone, so a test can tell which
// beam ran by which microphone comes out.
func passThrough() *Weights {
	w := &Weights{window: testWindow()}
	for band := range Bands {
		for beam := range Beams {
			w.fbf[band][beam][0][beam] = 1
		}
	}
	return w
}

func TestBeamFollowsTheLoudestMicrophone(t *testing.T) {
	b := passThrough().New()

	// Microphone 3 hears speech, everything else hears a little noise.
	frame := make([][]int16, Inputs)
	for m := range frame {
		frame[m] = make([]int16, testFrame)
	}
	for i := range testFrame {
		for m := range frame {
			frame[m][i] = int16(rand.N(200) - 100)
		}
	}

	for call := range 20 {
		for i := range testFrame {
			tt := float64(call*testFrame+i) / 16000
			frame[3][i] = int16(8000 * math.Sin(2*math.Pi*500*tt))
		}
		if got := b.Mix(frame); len(got) != testFrame {
			t.Fatalf("Mix returned %d samples, want %d", len(got), testFrame)
		}
	}

	if b.Beam() != 3 {
		t.Errorf("listening to beam %d, want 3 where the talker is", b.Beam())
	}
}

// A frame that is not a whole number of the bank's own frames still has to come out whole
// eventually, or samples would go missing every call.
func TestOddFrameSizesKeepEverySample(t *testing.T) {
	b := passThrough().New()

	frame := make([][]int16, Inputs)
	for m := range frame {
		frame[m] = make([]int16, 100)
	}

	total := 0
	for range 10 {
		total += len(b.Mix(frame))
	}
	if want := 10 * 100 / Hop * Hop; total < want {
		t.Errorf("returned %d samples of 1000, want at least %d", total, want)
	}
}
