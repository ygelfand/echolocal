package subband

// The bank is the standard polyphase pair. Analysis windows the last WindowLen samples with the
// prototype, folds them onto FFTLen by summing every FFTLen apart, and transforms; synthesis
// transforms back, spreads the result over WindowLen, windows it again and overlap-adds. Hop is half
// the transform, so five folds go in and five overlaps come out, and a band can be filtered without
// its neighbours aliasing into it.

// analysis turns one microphone's samples into subbands, Hop samples at a time.
type analysis struct {
	window []float32
	fft    *fft

	// hist is the last WindowLen samples, oldest first.
	hist []float32
	fold []complex64
}

func newAnalysis(window []float32, f *fft) *analysis {
	return &analysis{
		window: window,
		fft:    f,
		hist:   make([]float32, WindowLen),
		fold:   make([]complex64, FFTLen),
	}
}

// push takes Hop new samples and writes the frame's bands into out.
func (a *analysis) push(samples []float32, out []complex64) {
	copy(a.hist, a.hist[Hop:])
	copy(a.hist[WindowLen-Hop:], samples)

	for i := range a.fold {
		a.fold[i] = 0
	}
	for n, h := range a.window {
		a.fold[n%FFTLen] += complex(h*a.hist[n], 0)
	}

	a.fft.forward(a.fold)
	copy(out, a.fold[:Bands])
}

// synthesis turns subbands back into samples, Hop at a time.
type synthesis struct {
	window []float32
	fft    *fft

	spread []complex64
	// tail is what previous frames have already added to samples not yet returned.
	tail []float32
}

func newSynthesis(window []float32, f *fft) *synthesis {
	return &synthesis{
		window: window,
		fft:    f,
		spread: make([]complex64, FFTLen),
		tail:   make([]float32, WindowLen),
	}
}

// pull takes one frame's bands and returns the next Hop samples.
func (s *synthesis) pull(bands []complex64, out []float32) {
	// The transform is of a real signal, so the upper half is the lower half mirrored and conjugated.
	// Band 0 and the Nyquist band have no partner.
	s.spread[0] = complex(real(bands[0]), 0)
	for k := 1; k < Bands; k++ {
		s.spread[k] = bands[k]
		s.spread[FFTLen-k] = conj(bands[k])
	}
	s.spread[Bands] = 0

	s.fft.inverse(s.spread)

	// Overlap-add the windowed period, then hand back the oldest Hop samples.
	for n, h := range s.window {
		s.tail[n] += h * real(s.spread[n%FFTLen])
	}
	copy(out, s.tail[:Hop])
	copy(s.tail, s.tail[Hop:])
	for i := WindowLen - Hop; i < WindowLen; i++ {
		s.tail[i] = 0
	}
}

// bankGain is what a steady signal comes back scaled by after analysis and synthesis with the same
// prototype. Dividing it out leaves a mix comparable with the other mixers rather than one that
// needs its own volume.
//
// A constant folds to the prototype's own fold, so the gain at each position in the window is that
// fold weighted by the window again, summed over every position the frames land on. A prototype
// designed for this reconstructs exactly, which means each position gives the same answer and the
// average below is that answer.
func bankGain(window []float32) float32 {
	var folded [FFTLen]float32
	for n, h := range window {
		folded[n%FFTLen] += h
	}

	var sum float32
	for start := range Hop {
		for n := start; n < WindowLen; n += Hop {
			sum += window[n] * folded[n%FFTLen]
		}
	}
	return sum / Hop
}
