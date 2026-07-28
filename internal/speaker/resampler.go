package speaker

import (
	"math"
	"sync"

	"github.com/ygelfand/echolocal/internal/audio"
)

// Resampler stretches the pipeline's 16 kHz mono to the 48 kHz stereo the codec takes.
//
// Unlike the microphone mix, there is a right answer here: the band limited one measures two orders
// of magnitude better image rejection than repeating samples, and the difference is audible. The
// seam exists so it can be heard against the alternatives on this speaker rather than argued about,
// and so there is somewhere cheap to fall back to if the filter ever costs more than it is worth.
type Resampler interface {
	// Run appends the interleaved stereo result of mono to out. It is called with the voice lock
	// held and may keep state between calls, so an utterance delivered in chunks comes out as one
	// continuous signal.
	Run(mono []int16, out []int16) []int16

	// Reset drops that state, for when the next audio is unrelated to the last.
	Reset()

	// Clipped counts samples that came out past full scale.
	Clipped() uint64
}

// linear draws a straight line between input samples: the images land in the same place as a held
// sample's but come out attenuated, for two multiplies instead of voiceTaps.
type linear struct{ prev int16 }

func (l *linear) Reset()        { l.prev = 0 }
func (*linear) Clipped() uint64 { return 0 }

func (l *linear) Run(mono []int16, out []int16) []int16 {
	for _, s := range mono {
		step := (float32(s) - float32(l.prev)) / voicePhases
		for p := range voicePhases {
			v := int16(float32(l.prev) + step*float32(p+1))
			out = append(out, v, v)
		}
		l.prev = s
	}
	return out
}

// hold repeats each input sample, which is what the device did before the filter existed.
type hold struct{}

func (hold) Reset()          {}
func (hold) Clipped() uint64 { return 0 }

func (hold) Run(mono []int16, out []int16) []int16 {
	for _, s := range mono {
		for range voicePhases {
			out = append(out, s, s)
		}
	}
	return out
}

// sweepMs and sweepFrom/To are a test signal for comparing the options by ear. A rising sweep is
// the easiest thing to hear the difference on: the images of a tone at f land at the input rate
// minus f, so as the sweep rises its ghost falls, and a second tone moving the wrong way is obvious
// in a way that grit on speech is not.
const (
	sweepMs   = 1500
	sweepFrom = 300.0
	sweepTo   = 7000.0
)

// VoiceSweep is that signal, at the rate a pipeline sends, so it goes through the resampler exactly
// as a reply does.
func VoiceSweep() []int16 {
	frames := VoiceRate * sweepMs / 1000
	out := make([]int16, frames)

	var phase float64
	for i := range out {
		hz := sweepFrom + (sweepTo-sweepFrom)*float64(i)/float64(frames)
		phase += 2 * math.Pi * hz / VoiceRate

		// Half scale, with the same edge ramp the chimes use so neither end clicks.
		env := math.Min(1, math.Min(float64(i), float64(frames-i))/float64(VoiceRate/50))
		out[i] = int16(0.5 * env * math.MaxInt16 * math.Sin(phase))
	}
	return out
}

var (
	resamplersMu sync.RWMutex
	resamplers   = map[audio.Resampling]func() Resampler{
		audio.ResampleSinc:   func() Resampler { return &sinc{} },
		audio.ResampleLinear: func() Resampler { return &linear{} },
		audio.ResampleHold:   func() Resampler { return hold{} },
	}
	resamplerOrder = []audio.Resampling{audio.ResampleSinc, audio.ResampleLinear, audio.ResampleHold}
)

// Register adds a way to stretch the voice stream.
func Register(r audio.Resampling, make func() Resampler) {
	resamplersMu.Lock()
	defer resamplersMu.Unlock()

	if _, seen := resamplers[r]; !seen {
		resamplerOrder = append(resamplerOrder, r)
	}
	resamplers[r] = make
}

// Resamplings lists what this build can do, in the order it became available.
func Resamplings() []audio.Resampling {
	resamplersMu.RLock()
	defer resamplersMu.RUnlock()
	return append([]audio.Resampling(nil), resamplerOrder...)
}

// NewResampler builds one, falling back to the filter for anything this build does not have.
func NewResampler(r audio.Resampling) (Resampler, audio.Resampling) {
	resamplersMu.RLock()
	make, ok := resamplers[r]
	resamplersMu.RUnlock()

	if !ok {
		return resamplers[audio.ResampleSinc](), audio.ResampleSinc
	}
	return make(), r
}
