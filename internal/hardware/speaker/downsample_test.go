package speaker

import (
	"math"
	"testing"
)

func toneWave(rate int, n int, hz float64, amp float32) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amp * float32(math.Sin(2*math.Pi*hz*float64(i)/float64(rate))))
	}
	return out
}

func mid(samples []int16) []int16 {
	// The edges of a rate conversion see a partly-filled kernel. Sample the steady middle instead.
	lo, hi := len(samples)/4, len(samples)*3/4
	if hi <= lo {
		return samples
	}
	return samples[lo:hi]
}

func maxAbs(samples []int16) int32 {
	var m int32
	for _, s := range samples {
		a := int32(s)
		if a < 0 {
			a = -a
		}
		if a > m {
			m = a
		}
	}
	return m
}

func TestToVoiceRateLeavesThePipelineRateAlone(t *testing.T) {
	in := toneWave(VoiceRate, 1000, 440, 8000)
	out := ToVoiceRate(in, VoiceRate)
	if len(out) != len(in) {
		t.Fatalf("16 kHz input changed length: %d -> %d", len(in), len(out))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("sample %d changed: %d -> %d", i, in[i], out[i])
		}
	}
}

func TestToVoiceRateKeepsDC(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		in := make([]int16, rate*2)
		for i := range in {
			in[i] = 1000
		}
		out := ToVoiceRate(in, rate)
		for _, s := range mid(out) {
			if d := math.Abs(float64(s) - 1000); d > 20 {
				t.Fatalf("%d Hz: DC came out %d, want ~1000", rate, s)
			}
		}
	}
}

func TestToVoiceRateKeepsTheVoiceBand(t *testing.T) {
	const rate = 48000
	out := ToVoiceRate(toneWave(rate, rate, 1000, 30000), rate)
	if m := maxAbs(mid(out)); m < 20000 {
		t.Errorf("1 kHz tone came out at %d, want it near full scale", m)
	}
	if n := len(out); n < VoiceRate-80 || n > VoiceRate+80 {
		t.Errorf("a second of 48 kHz came out as %d samples, want ~%d", n, VoiceRate)
	}
}

func TestToVoiceRateDropsWhatWouldFold(t *testing.T) {
	const rate = 48000
	// 15 kHz sits above the pipeline's Nyquist: a decimator without a filter would fold it into the
	// voice band, an aliased 1 kHz. The low pass should leave barely a whisper of it.
	out := ToVoiceRate(toneWave(rate, rate, 15000, 30000), rate)
	if m := maxAbs(mid(out)); m > 1500 {
		t.Errorf("15 kHz tone survived decimation: max abs %d", m)
	}
}

func TestToVoiceRateUpsamples(t *testing.T) {
	const rate = 8000
	for _, tc := range []struct {
		name string
		in   []int16
		peak int32
	}{
		{"DC", func() []int16 {
			in := make([]int16, rate)
			for i := range in {
				in[i] = 1000
			}
			return in
		}(), 1000},
		{"tone", toneWave(rate, rate, 1500, 30000), 30000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := ToVoiceRate(tc.in, rate)
			want := len(tc.in) * VoiceRate / rate
			if n := len(out); n < want-80 || n > want+80 {
				t.Fatalf("upsample came back %d samples, want ~%d", n, want)
			}
			if m := maxAbs(mid(out)); m < tc.peak*8/10 {
				t.Errorf("came out at %d, want close to %d", m, tc.peak)
			}
			if m := maxAbs(mid(out)); m > tc.peak*12/10 {
				t.Errorf("measured %d, above what the input can reach", m)
			}
		})
	}
}
