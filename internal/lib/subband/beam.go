package subband

import "math"

// Bands the beam is chosen on, roughly 250 Hz to 4 kHz. Below that the array has no directivity to
// speak of and the room has most of its energy; above it, little of speech is left.
const (
	firstSpeechBand = 2
	lastSpeechBand  = 32

	// holdFrames is how long a direction has to keep winning before the beam switches, in frames of
	// Hop samples — about 160 ms, so a door closing off to one side does not swing the beam mid-word.
	holdFrames = 40
)

// Beamformer is one stream through the vendor's bank and weights. It is not safe for concurrent use.
type Beamformer struct {
	w     *Weights
	scale float32

	an  [Inputs]*analysis
	syn *synthesis

	// bands is the last Taps frames of every microphone, newest at at.
	bands [Taps][Inputs][Bands]complex64
	at    int

	beam   [Bands]complex64
	energy [Beams]float32

	// which beam is being listened to, which one is winning, and for how long.
	using, winning, held int

	carry [Inputs][]float32
	pcm   [Hop]float32
	out   []int16
}

// New builds a stream that uses these coefficients.
func (w *Weights) New() *Beamformer {
	f := newFFT(FFTLen)
	b := &Beamformer{
		w:     w,
		scale: float32(math.Pow(10, BoostDB/20)) / bankGain(w.window),
		syn:   newSynthesis(w.window, f),
	}
	for m := range b.an {
		b.an[m] = newAnalysis(w.window, f)
	}
	return b
}

// Mix combines the array through the beam that currently sounds most like the talker. Whole frames
// of Hop samples are what the bank works in, so a call returns what those frames produced and holds
// anything left over for the next one.
func (b *Beamformer) Mix(mics [][]int16) []int16 {
	if len(mics) < Inputs {
		return nil
	}
	b.out = b.out[:0]

	for m := range Inputs {
		for _, s := range mics[m] {
			b.carry[m] = append(b.carry[m], float32(s))
		}
	}

	at := 0
	for len(b.carry[0])-at >= Hop {
		for m := range Inputs {
			b.an[m].push(b.carry[m][at:at+Hop], b.bands[b.at][m][:])
		}
		b.frame()
		at += Hop
	}
	for m := range Inputs {
		b.carry[m] = append(b.carry[m][:0], b.carry[m][at:]...)
	}
	return b.out
}

// frame filters and sums one frame into every beam, picks one, and turns it back into samples.
func (b *Beamformer) frame() {
	for j := range Beams {
		var energy float32

		for band := range Bands {
			var acc complex64
			for tap := range Taps {
				past := &b.bands[(b.at-tap+Taps)%Taps]
				w := &b.w.fbf[band][j][tap]
				for m := range Inputs {
					acc += w[m] * past[m][band]
				}
			}

			if j == b.using {
				b.beam[band] = acc
			}
			if band >= firstSpeechBand && band <= lastSpeechBand {
				energy += real(acc)*real(acc) + imag(acc)*imag(acc)
			}
		}
		b.energy[j] = energy
	}

	b.steer()
	b.syn.pull(b.beam[:], b.pcm[:])

	for _, v := range b.pcm {
		b.out = append(b.out, clamp(v*b.scale))
	}
	b.at = (b.at + 1) % Taps
}

// steer switches beams once one has held the lead long enough.
func (b *Beamformer) steer() {
	loudest := 0
	for j := 1; j < Beams; j++ {
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
	if b.held++; b.held >= holdFrames {
		b.using, b.held = loudest, 0
	}
}

// Beam is which of the six is being listened to. The vendor's configuration gives the count and
// that each beam's nulls are two indices away, so they are 60 degrees apart in order, but not which
// direction beam 0 faces.
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
