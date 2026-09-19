package asp

import (
	"fmt"
	"math"
	"time"
)

// attack is how fast a band's gain comes down onto a signal that is too loud. The file gives releases
// only, so this one is ours: fast enough to catch a transient within a cycle of its own band, slow
// enough not to modulate the bass it is riding on.
const attack = 2 * time.Millisecond

// floorDB is the level below which a band counts as silent and its gain is left where it is. Tracking
// an envelope to negative infinity only costs precision.
const floorDB = -120

// mbclState is the four-band compressor and limiter, plus the limiter across the sum of them.
type mbclState struct {
	split *crossover
	bands []*bandState
	full  *limiter
	work  [][]float32
}

// bandState is one band's compressor followed by its limiter, the comp_ and lim_ settings the file
// gives each band, in series. The compressor's gain is carried in dB, so smoothing moves it at a rate
// that does not depend on how far down it already is.
type bandState struct {
	compThreshDB  float64
	compRatio     float64
	compGainMinDB float64

	// compInVol is a fixed gain applied to the signal before the compressor tracks it. radar uses
	// these to push some bands a few dB above the unit pipeline.
	compInVol float64
	// limInVol is a fixed gain applied after the compressor and before the band's limiter, so the
	// limiter sees the same level the vendor's reference build did.
	limInVol float64

	env    float64
	gainDB float64
	attack float64
	relCo  float64

	lim *limiter
}

// lookahead is how far ahead the limiter sees a peak coming, which is how long it has to bring the
// gain down before one arrives, and the delay it costs the playback path.
const lookahead = 2 * time.Millisecond

// limiter holds a ceiling by looking ahead: the signal is delayed, the gain each sample will need is
// worked out as it arrives, and the gain follows the lowest of those still to come. That gives it the
// lookahead to arrive smoothly, and a gain that moves smoothly is a gain that stays inaudible.
type limiter struct {
	ceiling float64
	gain    float64
	attack  float64
	relCo   float64

	// delay holds the samples not yet let out, and want the gain each of them will need. front is the
	// oldest of both. low holds the indices of want still in the running for the lowest, rising, so its
	// head is the lowest of everything still to come; it is a ring of its own to keep this allocation
	// free at 48 kHz.
	delay []float32
	want  []float64
	front int
	low   []int
	head  int
	tail  int
}

// pushLow drops everything that can no longer be the lowest and adds i, keeping low rising.
func (l *limiter) pushLow(i int, want float64) {
	for l.head != l.tail {
		back := (l.tail - 1 + len(l.low)) % len(l.low)
		if l.want[l.low[back]] < want {
			break
		}
		l.tail = back
	}
	l.low[l.tail] = i
	l.tail = (l.tail + 1) % len(l.low)
}

func newMBCL(m mbcl, rate int) (*mbclState, error) {
	if !m.PreFilterBypass {
		return nil, fmt.Errorf("asp: %s wants a prefilter we do not have", mbclFile)
	}

	s := &mbclState{
		split: newCrossover(m.Crossovers, rate),
		full:  newLimiter(m.Full.LimThresh, millis(m.Full.LimRelease), rate),
	}

	for _, b := range m.Bands {
		if b.CompRatio < 1 {
			return nil, fmt.Errorf("asp: %s has a compression ratio of %g", mbclFile, b.CompRatio)
		}
		s.bands = append(s.bands, &bandState{
			compThreshDB:  b.CompThresh,
			compRatio:     b.CompRatio,
			compGainMinDB: b.CompGainMin,
			compInVol:     math.Pow(10, b.CompInVol/20),
			limInVol:      math.Pow(10, b.LimInVol/20),
			attack:        smoothing(attack, rate),
			relCo:         smoothing(millis(b.LimRelease), rate),
			lim:           newLimiter(b.LimThresh, millis(b.LimRelease), rate),
		})
	}
	return s, nil
}

// newLimiter sizes the lookahead and the attack together: the gain has exactly the lookahead to reach
// what a peak asks for, so it covers almost all of that distance in that many samples.
func newLimiter(threshDB float64, rel time.Duration, rate int) *limiter {
	n := max(int(lookahead.Seconds()*float64(rate)), 1)
	return &limiter{
		ceiling: math.Pow(10, threshDB/20),
		gain:    1,
		attack:  1 - math.Exp(-4/float64(n)),
		relCo:   smoothing(rel, rate),
		delay:   make([]float32, n),
		want:    make([]float64, n),
		low:     make([]int, n+1),
	}
}

// smoothing is the per-sample coefficient of a one-pole that covers most of its distance in d.
func smoothing(d time.Duration, rate int) float64 {
	if d <= 0 {
		return 1
	}
	return 1 - math.Exp(-1/(d.Seconds()*float64(rate)))
}

// millis reads one of the file's times, which are all in milliseconds.
func millis(v float64) time.Duration { return time.Duration(v * float64(time.Millisecond)) }

func (s *mbclState) reset() {
	s.split.reset()
	for _, b := range s.bands {
		b.env, b.gainDB = 0, 0
		b.lim.reset()
	}
	s.full.reset()
}

// clone returns a copy of the MBCL with fresh history. Used to give each per-volume EQ chain its
// own compressor state, so swapping buckets does not smear one chain's gain onto the next.
func (s *mbclState) clone() *mbclState {
	out := &mbclState{
		split: s.split.clone(),
		full:  s.full.clone(),
		work:  make([][]float32, len(s.bands)),
	}
	for _, b := range s.bands {
		out.bands = append(out.bands, &bandState{
			compThreshDB:  b.compThreshDB,
			compRatio:     b.compRatio,
			compGainMinDB: b.compGainMinDB,
			compInVol:     b.compInVol,
			limInVol:      b.limInVol,
			attack:        b.attack,
			relCo:         b.relCo,
			lim:           b.lim.clone(),
		})
	}
	return out
}

func (l *limiter) clone() *limiter {
	return &limiter{
		ceiling: l.ceiling,
		gain:    l.gain,
		attack:  l.attack,
		relCo:   l.relCo,
		delay:   append([]float32(nil), l.delay...),
		want:    append([]float64(nil), l.want...),
		low:     append([]int(nil), l.low...),
	}
}

func (l *limiter) reset() {
	l.gain, l.front, l.head, l.tail = 1, 0, 0, 0
	clear(l.delay)
	clear(l.want)
}

// process splits the block into bands, rides each one's gain, and sums them back under a limiter.
func (s *mbclState) process(x []float32) {
	if len(s.work) != len(s.bands) || len(s.work[0]) != len(x) {
		s.work = make([][]float32, len(s.bands))
		for i := range s.work {
			s.work[i] = make([]float32, len(x))
		}
	}

	s.split.process(x, s.work)
	for i, b := range s.bands {
		b.process(s.work[i])
	}

	for i := range x {
		var sum float32
		for _, band := range s.work {
			sum += band[i]
		}
		x[i] = sum
	}
	s.full.process(x)
}

// process compresses one band and then holds it under its own ceiling.
func (b *bandState) process(x []float32) {
	for i, v := range x {
		s := float64(v) * b.compInVol
		level := b.track(s)

		want := 0.0
		if level > b.compThreshDB {
			want = -(level - b.compThreshDB) * (1 - 1/b.compRatio)
			want = math.Max(want, b.compGainMinDB)
		}

		b.gainDB = approach(b.gainDB, want, b.attack, b.relCo)
		x[i] = float32(s * math.Pow(10, b.gainDB/20) * b.limInVol)
	}
	b.lim.process(x)
}

// track follows the band's peak, falling at the release rate, and reports it in dB.
func (b *bandState) track(v float64) float64 {
	a := math.Abs(v)
	if a > b.env {
		b.env = a
	} else {
		b.env += (a - b.env) * b.relCo
	}
	if b.env <= 0 {
		return floorDB
	}
	return 20 * math.Log10(b.env)
}

func (l *limiter) process(x []float32) {
	n := len(l.delay)
	for i, v := range x {
		want := 1.0
		if a := math.Abs(float64(v)); a > l.ceiling {
			want = l.ceiling / a
		}

		l.want[l.front] = want
		l.pushLow(l.front, want)

		out := l.delay[l.front]
		l.delay[l.front] = v
		l.front = (l.front + 1) % n
		if l.low[l.head] == l.front {
			l.head = (l.head + 1) % len(l.low)
		}

		target := l.want[l.low[l.head]]
		if target < l.gain {
			l.gain += (target - l.gain) * l.attack
		} else {
			l.gain += (target - l.gain) * l.relCo
		}

		// The gain arrives ahead of the peak, so this only has to catch what rounding leaves.
		v := float64(out) * l.gain
		x[i] = float32(math.Max(-l.ceiling, math.Min(l.ceiling, v)))
	}
}

// approach moves a gain toward what was asked, quickly when that is further down and at the release
// rate when it is back up.
func approach(have, want, attack, release float64) float64 {
	co := release
	if want < have {
		co = attack
	}
	return have + (want-have)*co
}
