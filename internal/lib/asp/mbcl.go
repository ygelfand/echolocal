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

// bandState is one band's compressor and limiter, sharing one gain so that the two never fight: the
// compressor asks for a reduction, the limiter asks for whatever more it takes to stay under its
// threshold, and the sum is what the gain moves toward.
//
// The gain is carried in dB and converted per sample. Both ways of making that cheaper have been
// tried on the device and both are audible: holding the target across a run of samples dulls the
// bands with a fast release, and carrying the gain linearly so it is only a multiply adds noise.
// A quieter compressor is worth more here than a cheaper one.
type bandState struct {
	compThreshDB  float64
	compRatio     float64
	compGainMinDB float64
	limThreshDB   float64

	env    float64
	gainDB float64
	attack float64
	relCo  float64
}

// limiter is a gain that only ever comes down to hold a threshold.
type limiter struct {
	threshDB float64
	gainDB   float64
	attack   float64
	relCo    float64
}

func newMBCL(m mbcl, rate int) (*mbclState, error) {
	if !m.PreFilterBypass {
		return nil, fmt.Errorf("asp: %s wants a prefilter we do not have", mbclFile)
	}

	s := &mbclState{
		split: newCrossover(m.Crossovers, rate),
		full: &limiter{
			threshDB: m.Full.LimThresh,
			attack:   smoothing(attack, rate),
			relCo:    smoothing(millis(m.Full.LimRelease), rate),
		},
	}

	for _, b := range m.Bands {
		if b.CompInVol != 0 || b.LimInVol != 0 {
			return nil, fmt.Errorf("asp: %s asks for band input gain we do not apply", mbclFile)
		}
		if b.CompRatio < 1 {
			return nil, fmt.Errorf("asp: %s has a compression ratio of %g", mbclFile, b.CompRatio)
		}
		s.bands = append(s.bands, &bandState{
			compThreshDB:  b.CompThresh,
			compRatio:     b.CompRatio,
			compGainMinDB: b.CompGainMin,
			limThreshDB:   b.LimThresh,
			attack:        smoothing(attack, rate),
			relCo:         smoothing(millis(b.LimRelease), rate),
		})
	}
	return s, nil
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
	}
	s.full.gainDB = 0
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

// process rides one band's gain.
func (b *bandState) process(x []float32) {
	for i, v := range x {
		level := b.track(float64(v))

		want := 0.0
		if level > b.compThreshDB {
			want = -(level - b.compThreshDB) * (1 - 1/b.compRatio)
			want = math.Max(want, b.compGainMinDB)
		}
		if out := level + want; out > b.limThreshDB {
			want -= out - b.limThreshDB
		}

		b.gainDB = approach(b.gainDB, want, b.attack, b.relCo)
		x[i] = float32(float64(v) * math.Pow(10, b.gainDB/20))
	}
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
	for i, v := range x {
		want := 0.0
		if a := math.Abs(float64(v)); a > 0 {
			if level := 20 * math.Log10(a); level > l.threshDB {
				want = l.threshDB - level
			}
		}

		l.gainDB = approach(l.gainDB, want, l.attack, l.relCo)
		out := float64(v) * math.Pow(10, l.gainDB/20)

		// The gain is still on its way down when a transient arrives faster than the attack, so the
		// threshold is held here as well. Nothing downstream gets to clip.
		ceil := math.Pow(10, l.threshDB/20)
		x[i] = float32(math.Max(-ceil, math.Min(ceil, out)))
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
