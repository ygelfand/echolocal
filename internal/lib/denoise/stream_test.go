package denoise

import (
	"math"
	"testing"
)

const micRate = 16000

// The microphone hands out 20 ms frames, which is exactly the estimator's window at this rate.
func TestStreamMatchesTheMicrophoneFrame(t *testing.T) {
	s := NewStream(micRate)
	if got, want := s.f.Frame(), 320; got != want {
		t.Errorf("frame = %d, want %d", got, want)
	}
	if got, want := s.f.Hop(), 160; got != want {
		t.Errorf("hop = %d, want %d", got, want)
	}
}

// A stream is a delay, not a change of shape: whatever length of frame goes in comes out.
func TestStreamKeepsTheFrameLength(t *testing.T) {
	s := NewStream(micRate)
	for _, n := range []int{320, 160, 1, 7, 640, 1000} {
		buf := make([]int16, n)
		s.Apply(buf)
		if len(buf) != n {
			t.Errorf("a frame of %d came back as %d", n, len(buf))
		}
	}
}

func TestStreamPassesSilence(t *testing.T) {
	s := NewStream(micRate)
	for range 50 {
		buf := make([]int16, 320)
		s.Apply(buf)
		for i, v := range buf {
			if v != 0 {
				t.Fatalf("sample %d of silence came back as %d", i, v)
			}
		}
	}
}

// Streaming has to produce what the block path produces, delayed by the window it buffers. Real
// speech, since that is what the estimator distinguishes: a steady tone is noise by its definition,
// and correctly removed.
func TestStreamAgreesWithTheBlockPath(t *testing.T) {
	noisy, rate := wav(t, "in_SNR5_sp01.wav")
	want := process(New(rate), noisy)

	s := NewStream(rate)
	frame := s.f.Frame()
	got := make([]int16, 0, len(noisy))
	for at := 0; at+frame <= len(noisy); at += frame {
		buf := append([]int16(nil), noisy[at:at+frame]...)
		s.Apply(buf)
		got = append(got, buf...)
	}

	// The stream primes with one window, and the block path starts at its first whole hop.
	const lag = 1
	delay := frame * lag
	n := min(len(got)-delay, len(want))
	if n < rate {
		t.Fatalf("only %d samples to compare", n)
	}

	var num, a2, b2 float64
	for i := range n {
		a, b := float64(got[delay+i]), float64(want[i])
		num += a * b
		a2 += a * a
		b2 += b * b
	}
	corr := num / math.Sqrt(a2*b2)
	t.Logf("correlation with the block path %.6f over %d samples", corr, n)

	if corr < 0.999 {
		t.Errorf("streaming and block output correlate at only %.6f", corr)
	}
}

// The delay is one window, which is 20 ms whatever the rate. Wake detection, what Home Assistant
// transcribes and the recordings all inherit it, so it is a number worth pinning.
func TestStreamDelaysByOneWindow(t *testing.T) {
	for _, rate := range []int{8000, micRate} {
		s := NewStream(rate)
		frame := s.f.Frame()

		// Inside the opening frames, which pass through unfiltered, so the impulse stays findable.
		const at = 7
		var out []int16
		for f := range 6 {
			buf := make([]int16, frame)
			if f == 0 {
				buf[at] = 10000
			}
			s.Apply(buf)
			out = append(out, buf...)
		}

		found, peak := -1, int16(0)
		for i, v := range out {
			if v > peak {
				found, peak = i, v
			}
		}
		if peak != 10000 {
			t.Errorf("rate %d: the impulse came through at %d, want 10000", rate, peak)
		}
		if got := found - at; got != frame {
			t.Errorf("rate %d: delayed by %d samples, want %d", rate, got, frame)
		}
		if ms := 1000 * float64(frame) / float64(rate); ms != 20 {
			t.Errorf("rate %d: the window is %.1f ms, want 20", rate, ms)
		}
	}
}

func TestStreamForgetStartsOver(t *testing.T) {
	s := NewStream(micRate)
	buf := make([]int16, 320)
	for i := range buf {
		buf[i] = int16(1000 + i)
	}
	for range 20 {
		in := append([]int16(nil), buf...)
		s.Apply(in)
	}

	s.Forget()
	if s.f.opening != 0 || s.f.started {
		t.Errorf("opening = %d, started = %v; want 0, false", s.f.opening, s.f.started)
	}
	if len(s.pending) != 0 {
		t.Errorf("%d samples still pending", len(s.pending))
	}
}
