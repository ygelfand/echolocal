package denoise

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// wav reads a mono 16-bit PCM file, returning the samples and the rate. Only what testdata holds:
// the header is walked for the two chunks that matter rather than assumed to be 44 bytes.
func wav(t *testing.T, name string) ([]int16, int) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		t.Fatalf("%s is not a WAVE file", name)
	}

	var rate, channels, bits int
	for at := 12; at+8 <= len(raw); {
		id := string(raw[at : at+4])
		size := int(binary.LittleEndian.Uint32(raw[at+4 : at+8]))
		body := raw[at+8 : min(at+8+size, len(raw))]

		switch id {
		case "fmt ":
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			if channels != 1 || bits != 16 {
				t.Fatalf("%s is %d channels at %d bits, want mono 16", name, channels, bits)
			}
			out := make([]int16, len(body)/2)
			for i := range out {
				out[i] = int16(binary.LittleEndian.Uint16(body[2*i : 2*i+2]))
			}
			return out, rate
		}
		at += 8 + size + size%2
	}
	t.Fatalf("%s has no data chunk", name)
	return nil, 0
}

// process runs a whole file through the filter the way the reference does: a 20 ms window advancing
// by half of it, for as many whole hops as the input holds.
func process(f *Filter, in []int16) []int16 {
	frames := len(in)/f.Hop() - 1
	out := make([]int16, 0, frames*f.Hop())

	window := make([]float64, f.Frame())
	hop := make([]float64, f.Hop())

	for n := range frames {
		at := n * f.Hop()
		for i := range window {
			if at+i < len(in) {
				window[i] = float64(in[at+i])
			} else {
				window[i] = 0
			}
		}

		f.Push(window, hop)
		for _, v := range hop {
			out = append(out, clamp(v))
		}
	}
	return out
}

func clamp(v float64) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(math.Round(v))
}

func rmsDB(x []int16) float64 {
	var sum float64
	for _, s := range x {
		sum += float64(s) * float64(s)
	}
	if sum == 0 {
		return -200
	}
	return 20 * math.Log10(math.Sqrt(sum/float64(len(x)))/32768)
}

// The port has to agree with the implementation it came from, on the files that came with it. This is
// the test that says the algorithm was transcribed rather than approximated.
func TestMatchesTheReference(t *testing.T) {
	for _, snr := range []string{"5", "15"} {
		noisy, rate := wav(t, "in_SNR"+snr+"_sp01.wav")
		want, _ := wav(t, "out_SNR"+snr+"_sp01.wav")

		got := process(New(rate), noisy)
		if len(got) != len(want) {
			t.Fatalf("SNR %s: produced %d samples, the reference has %d", snr, len(got), len(want))
		}

		// Correlation rather than sample equality: the reference integrates E1 numerically and sums in
		// a different order, so the two agree in shape and level rather than bit for bit.
		var num, ours, theirs float64
		var worst float64
		for i := range want {
			a, b := float64(got[i]), float64(want[i])
			num += a * b
			ours += a * a
			theirs += b * b
			worst = math.Max(worst, math.Abs(a-b))
		}
		corr := num / math.Sqrt(ours*theirs)
		level := 20 * math.Log10(math.Sqrt(ours)/math.Sqrt(theirs))

		t.Logf("SNR %s: correlation %.5f, level %+.2f dB, largest sample difference %.0f", snr, corr, level, worst)

		if corr < 0.99 {
			t.Errorf("SNR %s: correlation with the reference is %.5f", snr, corr)
		}
		if math.Abs(level) > 0.5 {
			t.Errorf("SNR %s: level differs from the reference by %+.2f dB", snr, level)
		}
	}
}

func TestNoiseIsReduced(t *testing.T) {
	for _, snr := range []string{"5", "15"} {
		noisy, rate := wav(t, "in_SNR"+snr+"_sp01.wav")
		got := process(New(rate), noisy)

		before, after := rmsDB(noisy), rmsDB(got)
		t.Logf("SNR %s: %.1f dBFS in, %.1f dBFS out", snr, before, after)
		if after >= before {
			t.Errorf("SNR %s: the output is no quieter than the input", snr)
		}
	}
}

func TestForgetClearsTheEstimate(t *testing.T) {
	noisy, rate := wav(t, "in_SNR5_sp01.wav")
	f := New(rate)
	process(f, noisy)

	var held float64
	for _, n := range f.noise {
		held += n
	}
	if held == 0 {
		t.Fatal("the filter learned nothing about the room")
	}

	f.Forget()
	for k, n := range f.noise {
		if n != 0 {
			t.Fatalf("bin %d still holds %v", k, n)
		}
	}
	if f.opening != 0 || f.started {
		t.Errorf("opening = %d, started = %v; want 0, false", f.opening, f.started)
	}
}
