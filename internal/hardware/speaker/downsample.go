package speaker

import "math"

// The rate conversion runs polyphase: one kernel per sub-sample phase, with decSide taps on each
// side of the sample the output lands on. Unlike the upsample filter in resample.go, which is one
// fixed ratio and so one fixed kernel, this has to serve whatever rate the container carried, so
// the phase table is rebuilt for each rate.
const (
	downsamplePhases = 128
	downsampleSide   = 16
	downsampleTaps   = 2*downsampleSide + 1
)

// ToVoiceRate brings a mono stream at whatever rate a decoder handed over down to the pipeline's
// 16 kHz, which is what the rest of the voice path takes. It is the mirror image of the sinc
// upsample in resample.go: a windowed-sinc low pass at the lower Nyquist, sampled at the output
// instants, so the images a decimator would otherwise fold back into the voice band are gone before
// they can land. A signal already at the pipeline's rate passes through untouched.
func ToVoiceRate(mono []int16, rate int) []int16 {
	if rate == VoiceRate {
		return mono
	}
	if rate <= 0 || len(mono) == 0 {
		return mono
	}

	// The cutoff is the lower of the two Nyquists, half the smaller rate against the source: for a
	// downsampled source it is the anti-aliasing low pass just under the pipeline's Nyquist, for an
	// upsampled one the anti-image filter at the source's own Nyquist. Either way the transition
	// band is spent before anything can fold.
	fc := 0.5 * float64(min(rate, VoiceRate)) / float64(rate)

	// Phase kernels, keyed by the fractional part of where each output sample lands. One table per
	// rate — the cutoff depends on it — and a downloaded announcement is a handful of sine
	// evaluations rather than one per output sample. Normalized per phase so a flat signal comes
	// out at the level it went in.
	phase := make([][]float32, downsamplePhases)
	for p := range phase {
		frac := float64(p) / downsamplePhases
		tab := make([]float32, downsampleTaps)
		for k := range tab {
			t := frac + float64(k-downsampleSide)
			tab[k] = float32(2*fc) * float32(sincTaper(2*fc*t, t/downsampleSide))
		}
		var sum float32
		for _, v := range tab {
			sum += v
		}
		if sum != 0 {
			for i := range tab {
				tab[i] /= sum
			}
		}
		phase[p] = tab
	}

	step := float64(rate) / float64(VoiceRate)
	out := make([]int16, 0, len(mono)*VoiceRate/rate+1)
	for pos := float64(downsampleSide); pos < float64(len(mono)-downsampleSide); pos += step {
		base := int(pos)
		frac := pos - float64(base)

		p := phase[int(frac*downsamplePhases)&(downsamplePhases-1)]
		var acc float32
		for k, w := range p {
			acc += w * float32(mono[base+downsampleSide-k])
		}
		out = append(out, clamp16(acc))
	}
	return out
}

// sincTaper evaluates the windowed sinc used to build a rate-conversion kernel. x carries the
// passband scaling already (the 2fc factor fed in by the caller), w is the Hamming window's
// position, on [-1, 1], over the kernel's support.
func sincTaper(x, w float64) float64 {
	s := 1.0
	if x != 0 {
		s = math.Sin(math.Pi*x) / (math.Pi * x)
	}
	return s * (0.54 - 0.46*math.Cos(math.Pi*(w+1)))
}
