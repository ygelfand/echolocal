package aec

import (
	"math"
	"math/rand/v2"
	"strconv"
	"testing"
)

const frame = 320

// echoed passes ref through h and returns it as a microphone signal would arrive.
func echoed(ref []int16, h []float64) []int16 {
	out := make([]int16, len(ref))
	for i := range ref {
		var v float64
		for k, c := range h {
			if i-k >= 0 {
				v += c * float64(ref[i-k])
			}
		}
		out[i] = int16(v)
	}
	return out
}

func noiseAt(n int, amp float64, seed uint64) []int16 {
	out := make([]int16, n)
	r := rand.New(rand.NewPCG(seed, 7))
	for i := range out {
		out[i] = int16((r.Float64()*2 - 1) * amp)
	}
	return out
}

// erle is measured directly from the signals rather than trusting the Canceller's own report.
func erle(mic, out []int16) float64 {
	var d, e float64
	for i := range mic {
		d += float64(mic[i]) * float64(mic[i])
		e += float64(out[i]) * float64(out[i])
	}
	if e == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(d/e)
}

// run feeds everything through in frames, and returns the output of the last part only, with the
// filter frozen for it. Scoring on data the filter is still adapting to would be in-sample.
func run(t *testing.T, c *Canceller, mic, ref []int16, holdOut int) (gotMic, gotOut []int16) {
	t.Helper()

	split := len(mic) - holdOut
	for i := 0; i < len(mic); i += frame {
		end := min(i+frame, len(mic))
		if i >= split {
			c.SetAdapting(false)
		}

		out, err := c.Process(mic[i:end], ref[i:end])
		if err != nil {
			t.Fatal(err)
		}
		if i >= split {
			gotOut = append(gotOut, out...)
			gotMic = append(gotMic, mic[i:end]...)
		}
	}
	return gotMic, gotOut
}

// A pure delay is the easiest path there is, and the filter has to reach past it: the delay here is
// longer than the hardware's ~30 samples.
func TestCancelsAPureDelay(t *testing.T) {
	c, err := New(Config{Taps: 128, Mu: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	h := make([]float64, 40)
	h[30] = 0.5

	ref := noiseAt(48000, 8000, 1)
	mic := echoed(ref, h)

	gotMic, gotOut := run(t, c, mic, ref, 8000)
	if got := erle(gotMic, gotOut); got < 30 {
		t.Fatalf("erle %.1f dB on a pure delay, want at least 30", got)
	}
}

// A decaying random tail is the real shape: the measurement on the device showed the echo spread
// over tens of milliseconds rather than arriving as one reflection.
func TestCancelsADispersiveTail(t *testing.T) {
	c, err := New(Config{Taps: 512, Mu: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	r := rand.New(rand.NewPCG(3, 9))
	h := make([]float64, 300)
	for k := 30; k < len(h); k++ {
		h[k] = (r.Float64()*2 - 1) * 0.4 * math.Exp(-float64(k-30)/80)
	}

	ref := noiseAt(160000, 8000, 2)
	mic := echoed(ref, h)

	gotMic, gotOut := run(t, c, mic, ref, 16000)
	if got := erle(gotMic, gotOut); got < 20 {
		t.Fatalf("erle %.1f dB on a dispersive tail, want at least 20", got)
	}
}

// The filter must not treat a talking room as an echo of silence. This is the failure that matters:
// adapting against a silent reference drives the coefficients anywhere at all.
func TestSilentReferenceDoesNotDiverge(t *testing.T) {
	c, err := New(Config{Taps: 256, Mu: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	ref := make([]int16, 48000)
	mic := noiseAt(48000, 6000, 4)

	for i := 0; i < len(mic); i += frame {
		end := min(i+frame, len(mic))
		out, err := c.Process(mic[i:end], ref[i:end])
		if err != nil {
			t.Fatal(err)
		}
		for j := range out {
			if out[j] != mic[i+j] {
				t.Fatalf("output changed with no reference: mic %d became %d", mic[i+j], out[j])
			}
		}
	}

	for k, v := range c.w {
		if v != 0 {
			t.Fatalf("filter learned something from silence: w[%d] = %v", k, v)
		}
	}
}

// Near-end speech over the echo is what freezing exists for. A filter that keeps adapting through it
// is fitting the wrong signal, so it should end up worse than one that was told to stop.
func TestFreezingSurvivesDoubleTalk(t *testing.T) {
	h := make([]float64, 80)
	for k := 30; k < len(h); k++ {
		h[k] = 0.3 * math.Exp(-float64(k-30)/20)
	}

	ref := noiseAt(96000, 8000, 5)
	echo := echoed(ref, h)

	// Train both on echo alone.
	train := 64000
	var cs [2]*Canceller
	for i := range cs {
		c, err := New(Config{Taps: 256, Mu: 0.5})
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < train; j += frame {
			if _, err := c.Process(echo[j:j+frame], ref[j:j+frame]); err != nil {
				t.Fatal(err)
			}
		}
		cs[i] = c
	}

	// Then put a loud voice on top, one still adapting and one frozen.
	voice := noiseAt(len(ref), 9000, 6)
	both := make([]int16, len(ref))
	for i := range both {
		both[i] = echo[i] + voice[i]
	}
	cs[1].SetAdapting(false)

	// Measure how well each still cancels the echo afterwards, on echo alone.
	var got [2]float64
	for i, c := range cs {
		for j := train; j < len(both); j += frame {
			if _, err := c.Process(both[j:j+frame], ref[j:j+frame]); err != nil {
				t.Fatal(err)
			}
		}
		c.SetAdapting(false)

		var mics, outs []int16
		for j := 0; j < 16000; j += frame {
			out, err := c.Process(echo[j:j+frame], ref[j:j+frame])
			if err != nil {
				t.Fatal(err)
			}
			outs = append(outs, out...)
			mics = append(mics, echo[j:j+frame]...)
		}
		got[i] = erle(mics, outs)
	}

	if got[1] <= got[0] {
		t.Fatalf("freezing did not help through double talk: adapting %.1f dB, frozen %.1f dB", got[0], got[1])
	}
}

func TestResetForgets(t *testing.T) {
	c, err := New(Config{Taps: 128, Mu: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	h := make([]float64, 40)
	h[30] = 0.5
	ref := noiseAt(32000, 8000, 7)
	mic := echoed(ref, h)

	for i := 0; i+frame <= len(mic); i += frame {
		if _, err := c.Process(mic[i:i+frame], ref[i:i+frame]); err != nil {
			t.Fatal(err)
		}
	}
	if c.ERLE() <= 0 {
		t.Fatal("nothing was cancelled before the reset")
	}

	c.Reset()
	if c.ERLE() != 0 {
		t.Fatalf("ERLE survived a reset: %v", c.ERLE())
	}
	for k, v := range c.w {
		if v != 0 {
			t.Fatalf("the filter survived a reset: w[%d] = %v", k, v)
		}
	}

	// Only the first sample is a guarantee. Reset does not stop it learning, so by the end of this
	// same frame it has started predicting again, which is what it should do.
	out, err := c.Process(mic[:frame], ref[:frame])
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != mic[0] {
		t.Fatalf("the filter still cancelled the first sample after a reset: %d became %d", mic[0], out[0])
	}
}

// BenchmarkProcess is in units that mean something on the device: a frame is 20 ms of audio, so
// ns/op divided by 20,000,000 is the fraction of one core the filter costs while audio is playing.
func BenchmarkProcess(b *testing.B) {
	for _, taps := range []int{256, 512, 1024, 2048} {
		c, err := New(Config{Taps: taps, Mu: 0.3})
		if err != nil {
			b.Fatal(err)
		}

		mic, ref := noiseAt(frame, 6000, 1), noiseAt(frame, 8000, 2)

		b.Run(strconv.Itoa(taps), func(b *testing.B) {
			for range b.N {
				if _, err := c.Process(mic, ref); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/200000, "%core")
		})
	}
}

func TestRejectsBadConfigAndLengths(t *testing.T) {
	for _, cfg := range []Config{{Taps: 0, Mu: 0.3}, {Taps: -1, Mu: 0.3}, {Taps: 64, Mu: 0}, {Taps: 64, Mu: 2}} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) was accepted", cfg)
		}
	}

	c, err := New(Config{Taps: 64, Mu: 0.3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Process(make([]int16, 10), make([]int16, 11)); err != ErrLength {
		t.Fatalf("mismatched lengths gave %v, want ErrLength", err)
	}
}
