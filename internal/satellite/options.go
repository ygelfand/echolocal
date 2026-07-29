package satellite

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/mic"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
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
	gain       *esphome.Number
	leveling   *esphome.Switch
	resampling *esphome.Select
}

func newOptions(k *kit) *options {
	source, spk := k.Mic, k.Speaker
	o := &options{}

	o.microphone = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "microphone",
			Name:     "Microphone mixing",
			Icon:     "mdi:microphone-settings",
			Category: esphome.CategoryConfig,
		},
	}
	bind(o.microphone, mic.Mixings(),
		settings.Get().Microphone.MixingOr(settings.DefaultMixing),
		source.SetMixing, settings.SetMicMixing)

	o.leveling = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "mic_leveling",
			Name:     "Microphone leveling",
			Icon:     "mdi:signal-variant",
			Category: esphome.CategoryConfig,
		},
	}
	o.leveling.Set(settings.Get().Microphone.LevelingOr(settings.DefaultLeveling))
	o.leveling.OnCommand = func(on bool) {
		o.leveling.Set(on)
		source.SetLeveling(on)
		if err := settings.SetMicLeveling(on); err != nil {
			slog.Error("saving the microphone leveling failed", "err", err)
		}
	}

	o.gain = &esphome.Number{
		Base: esphome.Base{
			ObjectID: "microphone_gain",
			Name:     "Microphone gain",
			Icon:     "mdi:volume-plus",
			Category: esphome.CategoryConfig,
		},
		Min: 0, Max: 59, Step: 1, Unit: "dB",
		Mode: esphome.NumberBox,
	}
	o.gain.Set(float32(settings.Get().Microphone.GainOr(settings.DefaultMicGain)))
	o.gain.OnCommand = func(v float32) {
		o.gain.Set(v)
		if err := mic.SetGain(int(v)); err != nil {
			slog.Error("saving the microphone gain failed", "err", err)
		}
	}

	o.resampling = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "voice_resampling",
			Name:     "Voice resampling",
			Icon:     "mdi:sine-wave",
			Category: esphome.CategoryConfig,
		},
	}
	bind(o.resampling, speaker.Resamplings(),
		settings.Get().Speaker.ResamplingOr(settings.ResampleSinc),
		spk.SetResampling, settings.SetSpeakerResampling)

	return o
}

// bind wires a select to an enumerated setting: the options are the values' labels, choosing one
// applies it and stores whatever the device settled on, and saved is restored the same way. apply
// returns what it settled on, so a value this build cannot do falls back visibly rather than
// leaving Home Assistant showing something that is not running.
func bind[T settings.Labelled](sel *esphome.Select, values []T, saved T, apply func(T) T, save func(T) error) {
	sel.Options = settings.Labels(values)

	sel.OnCommand = func(label string) {
		want, ok := settings.ByLabel(values, label)
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
	return []esphome.Entity{o.microphone, o.gain, o.leveling, o.resampling}
}
