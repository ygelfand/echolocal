package satellite

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/state"
	"github.com/ygelfand/echolocal/internal/wake"
)

// EffectNone is the option that turns the wake animation off.
const EffectNone = "None"

// wakeFlash is how long a detection shows on the ring.
const wakeFlash = 1200 * time.Millisecond

// toneWake is what a detection sounds like: two rising notes, distinct from any button.
var toneWake = []speaker.Note{{Freq: 784, Ms: 60}, {Freq: 1175, Ms: 90}}

// wakeControl is the wake word's settings and its feedback. Detection itself runs elsewhere and
// calls Detected.
type wakeControl struct {
	enabled     *esphome.Switch
	effect      *esphome.Select
	tone        *esphome.Switch
	sensitivity *esphome.Number

	ring    *ringLight
	speaker *speaker.Player
}

func newWakeControl(ring *ringLight, spk *speaker.Player) *wakeControl {
	saved := state.Get().Settings

	w := &wakeControl{
		enabled: &esphome.Switch{
			Base: esphome.Base{ObjectID: "wake_detection", Name: "Wake word detection", Icon: "mdi:account-voice"},
		},
		effect: &esphome.Select{
			Base:    esphome.Base{ObjectID: "wake_effect", Name: "Wake word effect", Icon: "mdi:animation", Category: esphome.CategoryConfig},
			Options: append([]string{EffectNone}, led.EffectNames()...),
		},
		tone: &esphome.Switch{
			Base: esphome.Base{ObjectID: "wake_tone", Name: "Wake word tone", Icon: "mdi:music-note", Category: esphome.CategoryConfig},
		},
		sensitivity: &esphome.Number{
			Base: esphome.Base{ObjectID: "wake_sensitivity", Name: "Wake word sensitivity", Icon: "mdi:tune", Category: esphome.CategoryConfig},
			Min:  0.5, Max: 0.99, Step: 0.01,
			Mode: esphome.NumberBox,
		},
		ring:    ring,
		speaker: spk,
	}

	w.enabled.OnCommand = func(on bool) {
		w.enabled.Set(on)
		if err := state.SetWakeEnabled(on); err != nil {
			slog.Error("saving wake enabled failed", "err", err)
		}
		slog.Info("wake word", "enabled", on)
	}
	w.effect.OnCommand = func(v string) {
		w.effect.Set(v)
		if err := state.SetWakeEffect(v); err != nil {
			slog.Error("saving wake effect failed", "err", err)
		}
		slog.Info("wake effect", "set", v, "now", w.effect.Get())
	}
	w.tone.OnCommand = func(on bool) {
		w.tone.Set(on)
		if err := state.SetWakeTone(on); err != nil {
			slog.Error("saving wake tone failed", "err", err)
		}
	}

	w.sensitivity.OnCommand = func(v float32) {
		w.sensitivity.Set(v)
		if err := state.SetWakeSensitivity(float64(v)); err != nil {
			slog.Error("saving wake sensitivity failed", "err", err)
		}
		slog.Info("wake sensitivity", "cutoff", v)
	}

	w.enabled.Set(saved.Wake.EnabledOr(true))
	w.effect.Set(saved.Wake.EffectOr(led.EffectPulse))
	w.tone.Set(saved.Wake.ToneOr(true))
	w.sensitivity.Set(float32(saved.Wake.SensitivityOr(wake.DefaultCutoff)))

	slog.Info("wake settings restored", "enabled", w.enabled.Get(), "effect", w.effect.Get(),
		"tone", w.tone.Get(), "sensitivity", w.sensitivity.Get(), "options", w.effect.Options)
	return w
}

// Sensitivity is the threshold detection should use.
func (w *wakeControl) Sensitivity() float64 { return float64(w.sensitivity.Get()) }

func (w *wakeControl) entities() []esphome.Entity {
	return []esphome.Entity{w.enabled, w.effect, w.tone, w.sensitivity}
}

// Enabled reports whether detection should run.
func (w *wakeControl) Enabled() bool { return w.enabled.Get() }

// Chime sounds a detection, if the user wants a tone.
func (w *wakeControl) Chime() {
	if w.tone.Get() {
		chime(w.speaker, toneWake)
	}
}

// Flash shows a detection on the ring briefly, for a detection that starts no conversation.
func (w *wakeControl) Flash() {
	if effect := w.Effect(); effect != "" {
		w.ring.FlashEffect(effect, wakeFlash)
	}
}

// Hold starts the animation and leaves it running. A conversation uses this: the same animation
// runs from the wake word to the end of the reply, and the turn stops it.
func (w *wakeControl) Hold() {
	if effect := w.Effect(); effect != "" {
		w.ring.HoldEffect(effect)
	}
}

// Effect is the animation the user picked, empty when they turned it off.
func (w *wakeControl) Effect() string {
	if e := w.effect.Get(); e != EffectNone {
		return e
	}
	return ""
}
