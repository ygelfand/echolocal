// Package noise generates the sounds a device can be left playing: the three noise colours, and rain,
// wind, fire and the rest built out of them.
//
// Nothing here is a recording and nothing loops, which is the reason to generate rather than play a
// file: an hour of it never repeats, and it costs no storage.
package noise

import (
	"math"
	"math/rand/v2"
)

// Fill writes the next samples of a sound, between -1 and 1. It closes over the filters producing
// them, so samples have to be asked for in order and a second sound needs a second Fill.
type Fill func(dst []float32)

// Sound is one thing the device can generate. Each is a file of its own; adding one is that file and
// a line in the catalogue.
type Sound struct {
	Name string

	// RMS is what this sound comes out at before it is levelled — every one of these is a filter, or a
	// pile of them, with a gain of its own. It is measured rather than derived, and TestLevelsMatch is
	// what measures it.
	RMS float32

	// Peak replaces RMS for a sound that is mostly silence. Averages are the wrong thing to match there:
	// bringing crickets up to the level a steady sound holds would put each chirp at nearly full scale.
	Peak float32

	New func(g *Gen) Fill
}

// gain is what a sound is multiplied by to sit where the others do.
func (s Sound) gain() float32 {
	if s.Peak > 0 {
		return Loudest / s.Peak
	}
	return Level / s.RMS
}

// Level is what every sound is brought to, so that changing one for another is a change of character
// and not of loudness. What decides the number is the peaks rather than the loudness: these have crest
// factors up to seven, and clipping the top off an ocean swell is audible in a way that two quiet
// decibels are not.
const Level = 0.1

// Loudest is where the peak of a sound made of separate events is put instead, which lands it in the
// same place by ear without making the events themselves painful.
const Loudest = 0.6

var catalogue = []Sound{
	whiteNoise,
	pinkNoise,
	brownNoise,
	rainSound,
	oceanSound,
	brookSound,
	windSound,
	fireSound,
	cricketSound,
	fanSound,
	cabinSound,
}

var byName = map[string]Sound{}

func init() {
	for _, s := range catalogue {
		byName[s.Name] = s
	}
}

// Names lists what can be played.
func Names() []string {
	out := make([]string, 0, len(catalogue))
	for _, s := range catalogue {
		out = append(out, s.Name)
	}
	return out
}

// Has reports whether this build knows a name.
func Has(name string) bool { _, ok := byName[name]; return ok }

// New starts a sound at a sample rate, which is what the pitched parts of the sounds are placed
// against. It returns nil for a name this build does not have.
func New(name string, rate int) Fill { return Mix(rate, name) }

// Mix runs several at once, which is how a bed and a texture over it are played together — crickets
// under wind, droplets over a fan. Unknown names are dropped, and nil comes back only when none of
// them was known.
//
// Two steady sounds together would be 3 dB louder than one, so the sum is divided by the square root
// of how many of them there are — they are uncorrelated, which is what makes that the right number. A
// sparse sound does not count: it is levelled by its peaks and contributes almost nothing to the
// average, so attenuating for it would leave the bed quieter than it plays on its own.
func Mix(rate int, names ...string) Fill {
	return mixSeeded(rate, rand.Uint64(), names...)
}

func mixSeeded(rate int, seed uint64, names ...string) Fill {
	var (
		fills  []Fill
		gains  []float32
		sparse []bool
		steady int
	)
	for i, name := range names {
		s, ok := byName[name]
		if !ok {
			continue
		}

		g := &Gen{Rate: rate, rand: newRand(seed + uint64(i))}
		fills = append(fills, s.New(g))
		gains = append(gains, s.gain())
		sparse = append(sparse, s.Peak > 0)
		if s.Peak == 0 {
			steady++
		}
	}

	switch len(fills) {
	case 0:
		return nil
	case 1:
		return scaled(fills[0], gains[0])
	}

	together := 1 / float32(math.Sqrt(float64(max(1, steady))))
	for i := range gains {
		if !sparse[i] {
			gains[i] *= together
		}
	}
	buf := make([]float32, 0)

	return func(dst []float32) {
		if len(buf) < len(dst) {
			buf = make([]float32, len(dst))
		}
		clear(dst)

		for i, fill := range fills {
			into := buf[:len(dst)]
			fill(into)

			for j, v := range into {
				dst[j] += v * gains[i]
			}
		}
		for i, v := range dst {
			dst[i] = clamp(v)
		}
	}
}

func scaled(fill Fill, gain float32) Fill {
	return func(dst []float32) {
		fill(dst)
		for i, v := range dst {
			dst[i] = clamp(v * gain)
		}
	}
}

func clamp(v float32) float32 { return min(1, max(-1, v)) }
