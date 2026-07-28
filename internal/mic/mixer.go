package mic

import (
	"sync"

	"github.com/ygelfand/echolocal/internal/settings"
)

// Mixer reduces the array's channels to the one channel everything downstream reads. Wake detection
// and what Home Assistant transcribes come from the same mix, so they never disagree about what was
// heard.
//
// Which mix is best is a property of the room rather than of the code: an array this small gains
// against diffuse noise but cannot null a directional interferer, so a quiet room with a close
// talker may do as well on one microphone as on seven. That makes it a setting, and this the seam it
// turns on.
type Mixer interface {
	// Mix takes one frame per microphone, oldest sample first, and returns the combined frame. It is
	// called from the reader goroutine and may keep state between calls.
	Mix(mics [][]int16) []int16
}

// Centre is the middle microphone alone.
type Centre struct{}

func (Centre) Mix(mics [][]int16) []int16 {
	if len(mics) <= CentreMic {
		return nil
	}
	return mics[CentreMic]
}

// A mixer that needs more than belongs here — the device's own beamformer wants a filter bank and
// 460 KB of coefficients — lives in its own package and calls Register, so adding one costs nothing
// in this file.
var (
	mixersMu sync.RWMutex
	mixers   = map[settings.Mixing]func() Mixer{
		settings.MixCentre:   func() Mixer { return Centre{} },
		settings.MixDelaySum: func() Mixer { return NewBeamformer() },
	}
	order = []settings.Mixing{settings.MixCentre, settings.MixDelaySum}
)

// Register adds a way to combine the array.
func Register(m settings.Mixing, make func() Mixer) {
	mixersMu.Lock()
	defer mixersMu.Unlock()

	if _, seen := mixers[m]; !seen {
		order = append(order, m)
	}
	mixers[m] = make
}

// Mixings lists what this build can do, in the order it became available.
func Mixings() []settings.Mixing {
	mixersMu.RLock()
	defer mixersMu.RUnlock()
	return append([]settings.Mixing(nil), order...)
}

// NewMixer builds one, falling back to the array for anything this build does not have: a setting
// left over from another version should not leave the device deaf.
func NewMixer(m settings.Mixing) (Mixer, settings.Mixing) {
	mixersMu.RLock()
	make, ok := mixers[m]
	mixersMu.RUnlock()

	if !ok {
		return mixers[settings.MixDelaySum](), settings.MixDelaySum
	}
	return make(), m
}
