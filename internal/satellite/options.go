package satellite

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/audio"
	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/state"
)

// Settings are grouped by what their names begin with rather than by sub-device: Home Assistant
// shows a sub-device as a device of its own, so grouping that way would put a bare "Wake word"
// device in the list for every satellite in the house. A config category and a common prefix put
// them together on the one device instead.
//
// options are the device's tuning knobs: choices whose right answer depends on the room, the
// hardware or the ear rather than on the code. A knob that only takes effect on the next start is
// still worth having here, since init restarts echod.
type options struct {
	microphone *esphome.Select
	resampling *esphome.Select
}

func newOptions(source *mic.Source, spk *speaker.Player) *options {
	o := &options{}

	if source != nil {
		o.microphone = &esphome.Select{
			Base: esphome.Base{
				ObjectID: "microphone",
				Name:     "Microphone mixing",
				Icon:     "mdi:microphone-settings",
				Category: esphome.CategoryConfig,
			},
		}
		bind(o.microphone, mic.Mixings(),
			state.Get().Settings.Microphone.MixingOr(audio.MixDelaySum),
			source.SetMixing, state.SetMicMixing)
	}

	if spk != nil {
		o.resampling = &esphome.Select{
			Base: esphome.Base{
				ObjectID: "voice_resampling",
				Name:     "Voice resampling",
				Icon:     "mdi:sine-wave",
				Category: esphome.CategoryConfig,
			},
		}
		bind(o.resampling, speaker.Resamplings(),
			state.Get().Settings.Speaker.ResamplingOr(audio.ResampleSinc),
			spk.SetResampling, state.SetSpeakerResampling)
	}

	return o
}

// labelled is a setting whose values are a closed set that knows how to name itself.
type labelled interface{ Label() string }

// bind wires a select to one of those settings: the options are the values' labels, choosing one
// applies it and stores whatever the device settled on, and saved is restored the same way. apply
// returns what it settled on, so a value this build cannot do falls back visibly rather than
// leaving Home Assistant showing something that is not running.
func bind[T labelled](sel *esphome.Select, values []T, saved T, apply func(T) T, save func(T) error) {
	labels := make(map[string]T, len(values))
	for _, v := range values {
		sel.Options = append(sel.Options, v.Label())
		labels[v.Label()] = v
	}

	sel.OnCommand = func(label string) {
		want, ok := labels[label]
		if !ok {
			slog.Warn("unknown option", "setting", sel.ObjectID, "value", label)
			return
		}

		settled := apply(want)
		sel.Set(settled.Label())
		if err := save(settled); err != nil {
			slog.Error("saving a setting failed", "setting", sel.ObjectID, "err", err)
		}
		slog.Info("setting changed", "setting", sel.ObjectID, "using", settled)
	}

	settled := apply(saved)
	sel.Set(settled.Label())
	slog.Info("setting restored", "setting", sel.ObjectID, "using", settled, "available", sel.Options)
}

func (o *options) entities() []esphome.Entity {
	var ents []esphome.Entity
	for _, sel := range []*esphome.Select{o.microphone, o.resampling} {
		if sel != nil {
			ents = append(ents, sel)
		}
	}
	return ents
}
