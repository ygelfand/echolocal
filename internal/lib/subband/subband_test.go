package subband

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/ygelfand/echolocal/internal/lib/fft"
)

// testFrame is the frame the microphone hands out, 20 ms at 16 kHz. It is deliberately not a whole
// number of the wider bank's frames.
const testFrame = 320

// Every test here runs against both front ends. The two differ in every dimension the bank and the
// beamformer are written in terms of, so a geometry that only one of them handles is the failure
// this is looking for.
func eachGeometry(t *testing.T, f func(*testing.T, geometry)) {
	t.Helper()
	for _, g := range known {
		t.Run(g.name, func(t *testing.T) { f(t, g) })
	}
}

// testWindow is a prototype the bank reconstructs exactly: the root of a periodic Hann over one
// transform, zero beyond it. Its squares sum to one at this hop, and having no energy past FFTLen
// means no aliasing between folds, so any reconstruction error the test sees is the bank's own.
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

		// Tones well below the band the bank drops, so what comes back should be what went in.
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

		// The bank delays by everything but the newest frame.
		delay := g.WindowLen - g.Hop
		for i := delay; i < len(in); i++ {
			if diff := math.Abs(float64(out[i] - in[i-delay])); diff > 1e-3 {
				t.Fatalf("sample %d = %v, want %v", i, out[i], in[i-delay])
			}
		}
	})
}

// passThrough is one weight set per beam that takes a single microphone, so a test can tell which
// beam ran by which microphone comes out. Fire OS 6 has more beams than microphones, so they share.
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

func TestBeamFollowsTheLoudestMicrophone(t *testing.T) {
	eachGeometry(t, func(t *testing.T, g geometry) {
		// The talker is on the fourth microphone the weights use, whichever array channel that is.
		const talker = 3
		b := passThrough(g).New()

		frame := make([][]int16, g.Channels())
		for m := range frame {
			frame[m] = make([]int16, testFrame)
		}

		got := 0
		for call := range 20 {
			for i := range testFrame {
				for m := range frame {
					frame[m][i] = int16(rand.N(200) - 100)
				}
				tt := float64(call*testFrame+i) / 16000
				frame[g.mics[talker]][i] = int16(8000 * math.Sin(2*math.Pi*500*tt))
			}
			got += len(b.Mix(frame))
		}

		// Whole frames only, so a call can hold samples back, but not for long.
		if want := 20*testFrame - g.Hop; got < want {
			t.Errorf("Mix returned %d samples of %d, want at least %d", got, 20*testFrame, want)
		}
		if b.Beam()%g.Inputs() != talker {
			t.Errorf("listening to beam %d, which is microphone %d, want %d where the talker is",
				b.Beam(), b.Beam()%g.Inputs(), talker)
		}
	})
}

// A frame that is not a whole number of the bank's own frames still has to come out whole
// eventually, or samples would go missing every call.
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

// A device that has neither firmware's files gets an error rather than a beamformer.
func TestLoadWithoutCoefficients(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("an empty directory loaded")
	}
}
