package mic

import (
	"log/slog"
	"sync"

	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/subband"
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

// Center is the middle microphone alone.
type Center struct{}

func (Center) Mix(mics [][]int16) []int16 {
	if len(mics) <= CenterMic {
		return nil
	}
	return mics[CenterMic]
}

type mix struct {
	name settings.Mixing
	make func() Mixer
}

// What this build can do, in the order it is offered. The vendor's own beamformer is in the list
// only when its coefficients are there to read, and reading them waits until something asks: they
// are 460 KB of text on the vendor partition, and the mix may never be chosen.
var mixes = sync.OnceValue(func() []mix {
	out := []mix{
		{settings.MixCenter, func() Mixer { return Center{} }},
		{settings.MixDelaySum, func() Mixer { return NewBeamformer() }},
	}

	w, err := subband.Load(subband.VendorDir)
	if err != nil {
		slog.Error("vendor beamformer unavailable", "err", err)
		return out
	}
	slog.Info("vendor beamformer available", "bands", subband.Bands, "beams", subband.Beams)
	return append(out, mix{settings.MixBeamformer, func() Mixer { return w.New() }})
})

// Mixings lists what the array can be combined with.
func Mixings() []settings.Mixing {
	out := make([]settings.Mixing, 0, len(mixes()))
	for _, m := range mixes() {
		out = append(out, m.name)
	}
	return out
}

// NewMixer builds one, falling back to the array for anything this build does not have: a setting
// left over from another version, or from a device with the coefficients this one lacks, should not
// leave it deaf.
func NewMixer(m settings.Mixing) (Mixer, settings.Mixing) {
	for _, have := range mixes() {
		if have.name == m {
			return have.make(), m
		}
	}
	return Center{}, settings.DefaultMixing
}
