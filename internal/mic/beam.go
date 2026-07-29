package mic

import "math"

// Delay and sum across the seven microphones, steered at whichever direction is loudest.
//
// The array is a 36 mm ring of six microphones with a seventh in the middle, so the largest
// difference in arrival time across it is about 105 µs — 1.7 samples at 16 kHz. Integer shifts
// would quantise the whole useful range into three values, so the delays are interpolated.
//
// Summing seven microphones that agree on the speech and disagree on the room raises the speech
// against everything else. Steering is what makes them agree: pointed the wrong way, the same sum
// smears the speech instead.
const (
	// Radius of the perimeter ring and the bearing of ch0, from the geometry measured with
	// directional taps. Each subsequent microphone sits a further 60 degrees clockwise, and ch6 is
	// in the middle with no delay of its own.
	ringRadius   = 0.036
	firstMic     = 108 * math.Pi / 180
	micSpacing   = 60 * math.Pi / 180
	speedOfSound = 343.0

	// Beams the array is steered along. Six is one per perimeter microphone, which is as fine as an
	// aperture this small can usefully resolve.
	Beams = 6

	// beamHold is how long a direction has to keep winning before it is switched to, so a single
	// loud frame off to one side does not swing the beam mid-word. It is in frames of a captured
	// period, so eight is about 160 ms.
	//
	// That is worth waiting for when the mix is what Home Assistant hears, where a swing mid-word is
	// audible. It is only a cost when the answer is being drawn rather than listened to, so a finder
	// sets its own — see facing.go.
	beamHold = 8
)

// delayLine is one microphone's history, long enough for the largest steering delay plus the
// sample the interpolation reads ahead of it.
const delayLine = 8

// tap is one microphone's steering delay, split into the sample to read and the weight between it and
// the one before.
type tap struct {
	back int
	frac float32
}

// Beamformer combines the array into one channel. It is not safe for concurrent use.
type Beamformer struct {
	// taps[b][m] is where microphone m is read for beam b: how many whole samples back, and how far
	// between that sample and the one before it. Worked out once, because it is the innermost loop of the
	// whole array — six beams times seven microphones times every sample — and none of it changes.
	taps [Beams][Mics]tap

	hist  [Mics][delayLine]float32
	at    int
	beam  int
	votes int
	next  int

	// hold is how many frames in a row a direction has to win to be switched to.
	hold int

	// prev is each beam's last output, for measuring how much high frequency it kept.
	prev [Beams]float32
}

// NewBeamformer builds the steering delays for each beam.
func NewBeamformer() *Beamformer {
	b := &Beamformer{hold: beamHold}

	// A microphone facing the source hears it first, so it is the one held back the longest. The
	// common term keeps every delay positive without changing their differences.
	spread := ringRadius / speedOfSound * float64(Rate)
	for i := range Beams {
		bearing := float64(i) * 2 * math.Pi / Beams
		for m := range Mics {
			d := spread
			if m != CenterMic {
				d = spread * (1 + math.Cos(bearing-(firstMic+float64(m)*micSpacing)))
			}

			back := int(d)
			b.taps[i][m] = tap{back: back, frac: float32(d - float64(back))}
		}
	}
	return b
}

// Mix combines one frame of per-microphone samples into one channel, and picks the direction to
// steer at from the beams themselves.
func (b *Beamformer) Mix(mics [][]int16) []int16 {
	if len(mics) < Mics || len(mics[0]) == 0 {
		return nil
	}

	out := make([]int16, len(mics[0]))
	b.scan(mics, out, len(out))
	return out
}

// Look steers without mixing, for a caller that wants the direction and not the audio. samples is how
// much of the frame to listen to: the answer is one of six directions, so a slice of a frame settles it
// as well as the whole one and costs a fraction as much.
func (b *Beamformer) Look(mics [][]int16, samples int) {
	if len(mics) < Mics || len(mics[0]) == 0 {
		return
	}
	b.scan(mics, nil, samples)
}

// scan filters n samples into every beam, measures how much high frequency each kept, and steers at the
// winner. out takes the chosen beam when there is one to give.
func (b *Beamformer) scan(mics [][]int16, out []int16, n int) {
	if n > len(mics[0]) {
		n = len(mics[0])
	}
	energy := [Beams]float64{}

	for i := range n {
		for m := range Mics {
			b.hist[m][b.at] = float32(mics[m][i])
		}

		for beam := range Beams {
			v := b.sum(beam)

			// Judged on high frequency content rather than loudness. This array is far smaller
			// than a speech wavelength, so every beam is about as loud as every other and total
			// energy says nothing about direction. What steering does change is the top of the
			// band: microphones summed out of alignment comb against each other there, and the
			// aligned beam is the one that keeps it. The difference between successive samples is
			// a crude high pass, which is enough to tell them apart.
			d := v - b.prev[beam]
			b.prev[beam] = v
			energy[beam] += float64(d) * float64(d)

			if out != nil && beam == b.beam {
				out[i] = clamp(v)
			}
		}
		b.at = (b.at + 1) & (delayLine - 1)
	}

	b.steer(energy)
}

// sum reads each microphone at its steering delay and adds them, interpolating between the two
// samples the delay falls between.
//
// The history is a power of two long so the wrap is a mask rather than a division, which on a negative
// index gives the same answer for the same reason two's complement does.
func (b *Beamformer) sum(beam int) float32 {
	var acc float32
	for m := range Mics {
		t := b.taps[beam][m]

		// b.at holds the newest sample, so counting back from it is going back in time.
		older := b.hist[m][(b.at-t.back)&(delayLine-1)]
		oldest := b.hist[m][(b.at-t.back-1)&(delayLine-1)]
		acc += older + (oldest-older)*t.frac
	}
	return acc / Mics
}

// steer moves to the loudest beam once it has won often enough in a row.
func (b *Beamformer) steer(energy [Beams]float64) {
	best := 0
	for i := range Beams {
		if energy[i] > energy[best] {
			best = i
		}
	}

	if best == b.beam {
		b.votes = 0
		return
	}
	if best != b.next {
		b.next, b.votes = best, 0
	}
	b.votes++
	if b.votes >= b.hold {
		b.beam, b.votes = best, 0
	}
}

// Beam is the direction currently being listened to, as a beam index.
func (b *Beamformer) Beam() int { return b.beam }

func clamp(v float32) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}
