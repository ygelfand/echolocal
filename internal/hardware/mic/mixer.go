package mic

import (
	"log/slog"
	"sync"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/lib/subband"
)

type Mixer interface {
	Mix(mics [][]int16) []int16
}

type Center struct{}

func (Center) Mix(mics [][]int16) []int16 {
	if len(mics) <= CenterMic {
		return nil
	}
	return mics[CenterMic]
}

type mix struct {
	name config.Mixing
	make func() Mixer
}

var vendor = sync.OnceValue(func() *subband.Weights {
	if !beamforming {
		return nil
	}
	w, err := subband.Load(subband.VendorDir, tunings...)
	if err != nil {
		slog.Error("vendor beamformer unavailable", "err", err)
		return nil
	}
	slog.Info("vendor beamformer available", "tuning", w.Name(),
		"bands", w.Bands(), "beams", w.Beams(), "mics", w.Inputs())
	return w
})

var mixes = sync.OnceValue(func() []mix {
	out := []mix{{config.MixCenter, func() Mixer { return Center{} }}}
	if !beamforming {
		return out
	}
	if delaySum {
		out = append(out, mix{config.MixDelaySum, func() Mixer { return NewBeamformer() }})
	}

	w := vendor()
	if w == nil {
		return out
	}
	return append(out, mix{config.MixBeamformer, func() Mixer { return w.New() }})
})

func Mixings() []config.Mixing {
	out := make([]config.Mixing, 0, len(mixes()))
	for _, m := range mixes() {
		out = append(out, m.name)
	}
	return out
}

func NewMixer(m config.Mixing) (Mixer, config.Mixing) {
	for _, have := range mixes() {
		if have.name == m {
			return have.make(), m
		}
	}
	return Center{}, config.DefaultMixing
}
