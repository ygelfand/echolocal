package satellite

import (
	"log/slog"
	"slices"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
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
//
// Building one and restoring one are separate: newOptions wires the entities and what a command does,
// restore puts the saved values back. That way every saved value on the device goes back at the same
// point, in an order somebody chose — see restore.go.
type options struct {
	microphone *esphome.Select
	gain       *esphome.Number
	leveling   *esphome.Switch
	bluetooth  *esphome.Switch
	resampling *esphome.Select

	// What a failure shows on the ring. Momentary, so nothing has to be told when it changes: whoever
	// shows it reads the setting at the moment it happens. The one for a muted microphone lives with
	// the mute switch instead, since that one has to appear the moment it is chosen.
	trouble *esphome.Select

	// The hardware the knobs drive, held so restoring can apply them.
	mic *mic.Source
	spk *speaker.Player
	ble *bluetooth
}

func newOptions(k *kit) *options {
	o := &options{mic: k.Mic, spk: k.Speaker, ble: k.BLE}

	o.microphone = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "microphone",
			Name:     "Microphone mixing",
			Icon:     "mdi:microphone-settings",
			Category: esphome.CategoryConfig,
		},
	}
	bind(o.microphone, mic.Mixings(), o.mic.SetMixing, settings.SetMicMixing)

	o.leveling = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "mic_leveling",
			Name:     "Microphone leveling",
			Icon:     "mdi:signal-variant",
			Category: esphome.CategoryConfig,
		},
	}
	o.leveling.OnCommand = func(on bool) {
		o.leveling.Set(on)
		o.mic.SetLeveling(on)
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
	bind(o.resampling, speaker.Resamplings(), o.spk.SetResampling, settings.SetSpeakerResampling)

	o.bluetooth = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "bluetooth_proxy",
			Name:     "Bluetooth proxy",
			Icon:     "mdi:bluetooth",
			Category: esphome.CategoryConfig,
		},
	}
	o.bluetooth.Set(settings.Get().Bluetooth.ProxyOr(settings.DefaultBluetoothProxy))
	o.bluetooth.OnCommand = func(on bool) {
		o.bluetooth.Set(on)
		if err := settings.SetBluetoothProxy(on); err != nil {
			slog.Error("saving the bluetooth proxy setting failed", "err", err)
			return
		}
		o.ble.Enable(on)
	}

	o.trouble = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "ring_trouble",
			Name:     "Ring on failure",
			Icon:     "mdi:alert-circle",
			Category: esphome.CategoryConfig,
		},
	}
	bindEffect(o.trouble, led.EffectNames(), nil, settings.SetRingTrouble)

	return o
}

// restore puts the knobs back. The analog microphone gain is only published here: the capture service
// applies it when it opens the device, which is the only moment it can be set.
func (o *options) restore(saved settings.Stored) {
	restoreBound(o.microphone, mic.Mixings(),
		saved.Microphone.MixingOr(settings.DefaultMixing), o.mic.SetMixing,
		saved.Microphone.Mixing != nil)

	leveling := saved.Microphone.LevelingOr(settings.DefaultLeveling)
	o.leveling.Set(leveling)
	o.mic.SetLeveling(leveling)
	slog.Info("restored", "what", o.leveling.ObjectID, "using", leveling,
		"from", from(saved.Microphone.Leveling != nil))

	gain := saved.Microphone.GainOr(settings.DefaultMicGain)
	o.gain.Set(float32(gain))
	slog.Info("restored", "what", o.gain.ObjectID, "using", gain,
		"from", from(saved.Microphone.Gain != nil))

	restoreBound(o.resampling, speaker.Resamplings(),
		saved.Speaker.ResamplingOr(settings.ResampleSinc), o.spk.SetResampling,
		saved.Speaker.Resampling != nil)

	restoreEffect(o.trouble, saved.Ring.TroubleOr(settings.DefaultTrouble), nil,
		settings.SetRingTrouble, saved.Ring.Trouble != nil)
}

// bind wires a select to an enumerated setting: the options are the values' labels, and choosing one
// applies it and stores whatever the device settled on. apply returns what it settled on, so a value
// this build cannot do falls back visibly rather than leaving Home Assistant showing something that is
// not running.
func bind[T settings.Labelled](sel *esphome.Select, values []T, apply func(T) T, save func(T) error) {
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
}

// restoreBound applies a saved value and publishes what the device settled on, which is not always the
// same thing: a value this build cannot do reverts in the interface rather than sitting there looking
// as though it is running.
func restoreBound[T settings.Labelled](sel *esphome.Select, values []T, saved T, apply func(T) T, stored bool) {
	settled := apply(saved)
	sel.Set(settled.Label())
	slog.Info("restored", "what", sel.ObjectID, "using", settled,
		"asked", saved, "from", from(stored))
}

// The device shows animations of its own in several places — a wake word, a failure, a muted
// microphone, a room worth watching — and each is a select naming one from the catalogue. These are
// how, so that the places agree on what a name means.

// settleEffect puts a name on a select and reports what it settled on, empty for None. A name this
// build does not have — stored by one that did, or hand-edited — settles to None rather than being
// offered: unsettled, it reaches RunEffect, fails, and leaves the ring dark for as long as whatever
// wanted it holds the claim.
func settleEffect(sel *esphome.Select, name string) string {
	if name == "" {
		name = EffectNone
	}
	if !slices.Contains(sel.Options, name) {
		slog.Warn("no such effect, showing nothing instead", "setting", sel.ObjectID, "effect", name)
		name = EffectNone
	}

	sel.Set(name)
	if name == EffectNone {
		return ""
	}
	return name
}

// bindEffect offers a catalogue list on a select and saves what is chosen. apply is for a choice
// something has to be told about as it happens, such as the room reaction, which holds a claim; it is
// nil for the rest, where whoever shows the animation looks the name up when the moment comes.
func bindEffect(sel *esphome.Select, choices []string, apply func(string), save func(string) error) {
	sel.Options = append([]string{EffectNone}, choices...)

	sel.OnCommand = func(chosen string) {
		settled := settleEffect(sel, chosen)
		if apply != nil {
			apply(settled)
		}
		if err := save(settled); err != nil {
			slog.Error("saving a setting failed", "setting", sel.ObjectID, "err", err)
		}
		slog.Info("setting changed", "setting", sel.ObjectID, "using", sel.Get())
	}
}

// restoreEffect puts a saved name back, correcting one this build does not have.
//
// A correction is written back, because what reads these at the moment they are wanted is the setting
// rather than the select — a failure indication has no way to reach the entity. Leaving the two
// disagreeing is how a stored name that cannot run keeps being tried.
func restoreEffect(sel *esphome.Select, saved string, apply func(string), save func(string) error, stored bool) {
	settled := settleEffect(sel, saved)
	if apply != nil {
		apply(settled)
	}
	if settled != saved {
		if err := save(settled); err != nil {
			slog.Error("saving a corrected setting failed", "setting", sel.ObjectID, "err", err)
		}
	}
	slog.Info("restored", "what", sel.ObjectID, "using", sel.Get(), "from", from(stored))
}

// chosenEffect is what a select settled on, empty when it says None.
func chosenEffect(sel *esphome.Select) string {
	if name := sel.Get(); name != EffectNone {
		return name
	}
	return ""
}

func (o *options) entities() []esphome.Entity {
	return []esphome.Entity{o.microphone, o.gain, o.leveling, o.resampling, o.trouble, o.bluetooth}
}
