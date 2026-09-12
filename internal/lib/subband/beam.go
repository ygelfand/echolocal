package subband

import (
	"math"

	"github.com/ygelfand/echolocal/internal/lib/fft"
)

// Beamformer is one stream through the vendor's bank and weights. It is not safe for concurrent use.
type Beamformer struct {
	w     *Weights
	g     geometry
	scale float32

	an  []*analysis
	syn *synthesis

	// bands is the last Taps frames of every microphone, newest at at, indexed by frame below.
	bands []complex64
	at    int

	beam   []complex64
	energy []float32

	// which beam is being listened to, which one is winning, and for how long.
	using, winning, held int

	carry [][]float32
	pcm   []float32
	out   []int16
}

// New builds a stream that uses these coefficients.
func (w *Weights) New() *Beamformer {
	g := w.g
	f := fft.New(g.FFTLen)

	b := &Beamformer{
		w:      w,
		g:      g,
		scale:  float32(math.Pow(10, g.boostDB/20)) / bankGain(g, w.window),
		an:     make([]*analysis, g.Inputs()),
		syn:    newSynthesis(g, w.window, f),
		bands:  make([]complex64, g.Taps*g.Inputs()*g.Bands),
		beam:   make([]complex64, g.Bands),
		energy: make([]float32, g.Beams),
		carry:  make([][]float32, g.Inputs()),
		pcm:    make([]float32, g.Hop),
	}
	for m := range b.an {
		b.an[m] = newAnalysis(g, w.window, f)
	}
	return b
}

// frameBands is one microphone's bands from one of the Taps frames held, and tapBands every
// microphone's, laid out mic-major so the filter-and-sum walks it in order.
func (b *Beamformer) frameBands(tap, mic int) []complex64 {
	at := (tap*b.g.Inputs() + mic) * b.g.Bands
	return b.bands[at : at+b.g.Bands]
}

func (b *Beamformer) tapBands(tap int) []complex64 {
	n := b.g.Inputs() * b.g.Bands
	return b.bands[tap*n : (tap+1)*n]
}

// Mix combines the array through the beam that currently sounds most like the talker. Whole frames
// of Hop samples are what the bank works in, so a call returns what those frames produced and holds
// anything left over for the next one.
//
// The weights are generated for a selection of the array rather than all of it, so what arrives is
// indexed through that selection: on Fire OS 6 four of the seven microphones reach the beamformer.
func (b *Beamformer) Mix(mics [][]int16) []int16 {
	if len(mics) < b.g.Channels() {
		return nil
	}
	b.out = b.out[:0]

	for m, ch := range b.g.mics {
		for _, s := range mics[ch] {
			b.carry[m] = append(b.carry[m], float32(s))
		}
	}

	at := 0
	for len(b.carry[0])-at >= b.g.Hop {
		for m := range b.g.Inputs() {
			b.an[m].push(b.carry[m][at:at+b.g.Hop], b.frameBands(b.at, m))
		}
		b.frame()
		at += b.g.Hop
	}
	for m := range b.g.Inputs() {
		b.carry[m] = append(b.carry[m][:0], b.carry[m][at:]...)
	}
	return b.out
}

// frame filters and sums one frame into every beam, picks one, and turns it back into samples.
func (b *Beamformer) frame() {
	g := b.g
	first, last := g.firstSpeechBand(), g.lastSpeechBand()

	for j := range g.Beams {
		var energy float32

		for band := range g.Bands {
			var acc complex64
			for tap := range g.Taps {
				past := b.tapBands((b.at - tap + g.Taps) % g.Taps)
				w := b.w.weight(band, j, tap)
				for m := range g.Inputs() {
					acc += w[m] * past[m*g.Bands+band]
				}
			}

			if j == b.using {
				b.beam[band] = acc
			}
			if band >= first && band <= last {
				energy += real(acc)*real(acc) + imag(acc)*imag(acc)
			}
		}
		b.energy[j] = energy
	}

	b.steer()
	b.syn.pull(b.beam, b.pcm)

	for _, v := range b.pcm {
		b.out = append(b.out, clamp(v*b.scale))
	}
	b.at = (b.at + 1) % g.Taps
}

// steer switches beams once one has held the lead long enough.
func (b *Beamformer) steer() {
	loudest := 0
	for j := 1; j < b.g.Beams; j++ {
		if b.energy[j] > b.energy[loudest] {
			loudest = j
		}
	}

	if loudest == b.using {
		b.held = 0
		return
	}
	if loudest != b.winning {
		b.winning, b.held = loudest, 0
		return
	}
	if b.held++; b.held >= b.g.holdFrames() {
		b.using, b.held = loudest, 0
	}
}

// Beam is which one is being listened to. The vendor's configuration gives the count and that each
// beam's nulls are two indices away, so they are evenly spaced in order, but not which direction beam
// 0 faces.
func (b *Beamformer) Beam() int { return b.using }

func clamp(v float32) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
}
