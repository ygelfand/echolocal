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
	// loud frame off to one side does not swing the beam mid-word.
	beamHold = 8
)

// delayLine is one microphone's history, long enough for the largest steering delay plus the
// sample the interpolation reads ahead of it.
const delayLine = 8

// Beamformer combines the array into one channel. It is not safe for concurrent use.
type Beamformer struct {
	// delays[b][m] is how far microphone m is held back for beam b, in samples.
	delays [Beams][Mics]float64

	hist  [Mics][delayLine]float32
	at    int
	beam  int
	votes int
	next  int

	// prev is each beam's last output, for measuring how much high frequency it kept.
	prev [Beams]float32
}

// NewBeamformer builds the steering delays for each beam.
func NewBeamformer() *Beamformer {
	b := &Beamformer{}

	// A microphone facing the source hears it first, so it is the one held back the longest. The
	// common term keeps every delay positive without changing their differences.
	spread := ringRadius / speedOfSound * float64(Rate)
	for i := range Beams {
		bearing := float64(i) * 2 * math.Pi / Beams
		for m := range Mics {
			if m == CenterMic {
				b.delays[i][m] = spread
				continue
			}
			b.delays[i][m] = spread * (1 + math.Cos(bearing-(firstMic+float64(m)*micSpacing)))
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
	n := len(mics[0])

	out := make([]int16, n)
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

			if beam == b.beam {
				out[i] = clamp(v)
			}
		}
		b.at = (b.at + 1) % delayLine
	}

	b.steer(energy)
	return out
}

// sum reads each microphone at its steering delay and adds them, interpolating between the two
// samples the delay falls between.
func (b *Beamformer) sum(beam int) float32 {
	var acc float32
	for m := range Mics {
		d := b.delays[beam][m]
		whole := int(d)
		frac := float32(d - float64(whole))

		// b.at holds the newest sample, so counting back from it is going back in time.
		older := b.hist[m][((b.at-whole)%delayLine+delayLine)%delayLine]
		oldest := b.hist[m][((b.at-whole-1)%delayLine+delayLine)%delayLine]
		acc += older + (oldest-older)*frac
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
	if b.votes >= beamHold {
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
