// Package microphone is the array's tuning knobs: how the seven microphones are combined, how hard
// they are driven, and whether the mix is levelled before anything listens to it.
package microphone

import (
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
)

func init() {
	component.Register(component.Device, Get(), component.Order(5))
}

// Names begin with the same word rather than being grouped into a sub-device: Home Assistant shows a
// sub-device as a device of its own, which would put a bare "Microphone" device in the list for
// every satellite in the house.
type Microphone struct {
	mixing   *esphome.Select
	gain     *esphome.Number
	leveling *esphome.Switch
}

var (
	once   sync.Once
	shared *Microphone
)

func Get() *Microphone {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Microphone {
	m := &Microphone{
		mixing: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "microphone_mixing",
				Name:     "Microphone mixing",
				Icon:     "mdi:microphone-settings",
				Category: esphome.CategoryConfig,
			},
		},
		leveling: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "microphone_leveling",
				Name:     "Microphone leveling",
				Icon:     "mdi:signal-variant",
				Category: esphome.CategoryConfig,
			},
		},
		gain: &esphome.Number{
			Base: esphome.Base{
				ObjectID: "microphone_gain",
				Name:     "Microphone gain",
				Icon:     "mdi:volume-plus",
				Category: esphome.CategoryConfig,
			},
			Min: 0, Max: 59, Step: 1, Unit: "dB",
			Mode: esphome.NumberBox,
		},
	}

	source := mic.Get()
	component.Bind(m.mixing, mic.Mixings(), source.SetMixing, config.Set().Microphone().Mixing)

	m.leveling.OnCommand = func(on bool) {
		m.leveling.Set(on)
		source.SetLeveling(on)
		if err := config.Set().Microphone().Leveling(on); err != nil {
			slog.Error("saving the microphone leveling failed", "err", err)
		}
	}

	m.gain.OnCommand = func(v float32) {
		m.gain.Set(v)
		if err := mic.SetGain(int(v)); err != nil {
			slog.Error("saving the microphone gain failed", "err", err)
		}
	}
	return m
}

func (m *Microphone) Name() string { return "microphone settings" }

func (m *Microphone) Entities() []esphome.Entity {
	return []esphome.Entity{m.mixing, m.gain, m.leveling}
}

// Restore puts the knobs back. The analog gain is only published here: the capture service applies
// it when it opens the device, which is the only moment it can be set.
func (m *Microphone) Restore(c config.Config) {
	source := mic.Get()

	component.Restore(m.mixing, c.Microphone.Mixing, source.SetMixing)

	m.leveling.Set(c.Microphone.Leveling)
	source.SetLeveling(c.Microphone.Leveling)
	slog.Info("restored", "what", m.leveling.ObjectID, "using", c.Microphone.Leveling)

	m.gain.Set(float32(c.Microphone.Gain))
	slog.Info("restored", "what", m.gain.ObjectID, "using", c.Microphone.Gain)
}
