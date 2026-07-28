package satellite

import (
	"fmt"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
)

// EffectNone is the option that turns the wake animation off.
const EffectNone = "None"

// wakeFlash is how long a detection shows on the ring.
const wakeFlash = 1200 * time.Millisecond

// wakeControl is the wake words' settings and their feedback. Detection itself runs elsewhere and
// calls Detected.
//
// There is no switch for detection: a slot set to no wake word is off, and every slot off is
// detection off, which is the same thing Home Assistant's own wake word selects already say. A
// second control for it could only disagree with them.
type wakeControl struct {
	backend *esphome.Select
	slots   []wakeSlot

	ring    *ringLight
	speaker *speaker.Player

	// onBackend is called after the backend changes, to reload the wake words that backend was last
	// used with and to make Home Assistant re-read which models are on offer.
	onBackend func(settings.WakeBackend)
}

// wakeSlot is the per-slot configuration. Home Assistant pairs each of its wake word slots with its
// own pipeline, so the feedback is per slot too: two wake words can mean two different assistants,
// and they should not look or sound the same.
type wakeSlot struct {
	threshold *esphome.Number
	tone      *esphome.Select
	effect    *esphome.Select
}

func newWakeControl(ring *ringLight, spk *speaker.Player, backends []settings.WakeBackend, slots int) *wakeControl {
	w := &wakeControl{
		backend: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "wake_backend",
				Name:     "Wake word engine",
				Icon:     "mdi:cog-outline",
				Category: esphome.CategoryConfig,
			},
			Options: settings.Labels(backends),
		},
		ring:    ring,
		speaker: spk,
	}

	w.backend.OnCommand = func(label string) {
		want, ok := settings.ByLabel(backends, label)
		if !ok {
			slog.Warn("unknown wake backend", "value", label)
			return
		}
		if err := settings.SetWakeBackend(want); err != nil {
			slog.Error("saving the wake backend failed", "err", err)
		}
		w.backend.Set(want.Label())

		// The slots' saved values are per backend, so everything they show has to be re-read.
		w.restoreSlots()
		if w.onBackend != nil {
			w.onBackend(want)
		}
		slog.Info("wake backend", "using", want)
	}

	for i := range slots {
		w.slots = append(w.slots, w.newSlot(i))
	}

	w.backend.Set(settings.Get().Wake.BackendOr(settings.DefaultBackend).Label())
	w.restoreSlots()
	return w
}

// newSlot builds one slot's entities. They are numbered from one and named to match Home Assistant's
// own wake word selects, which is what the user is pairing them with.
func (w *wakeControl) newSlot(n int) wakeSlot {
	prefix := fmt.Sprintf("Wake word %d", n+1)

	s := wakeSlot{
		threshold: &esphome.Number{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("wake_threshold_%d", n+1),
				Name:     prefix + " sensitivity",
				Icon:     "mdi:tune",
				Category: esphome.CategoryConfig,
			},
			Min: 0.5, Max: 0.99, Step: 0.01,
			Mode: esphome.NumberBox,
		},
		tone: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("wake_tone_%d", n+1),
				Name:     prefix + " tone",
				Icon:     "mdi:music-note",
				Category: esphome.CategoryConfig,
			},
			Options: settings.Labels(WakeTones()),
		},
		effect: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("wake_effect_%d", n+1),
				Name:     prefix + " effect",
				Icon:     "mdi:animation",
				Category: esphome.CategoryConfig,
			},
			Options: append([]string{EffectNone}, led.EffectNames()...),
		},
	}

	s.threshold.OnCommand = func(v float32) {
		s.threshold.Set(v)
		if err := settings.SetWakeThreshold(n, float64(v)); err != nil {
			slog.Error("saving the wake threshold failed", "slot", n+1, "err", err)
		}
		slog.Info("wake sensitivity", "slot", n+1, "cutoff", v)
	}
	s.tone.OnCommand = func(label string) {
		tone, ok := settings.ByLabel(WakeTones(), label)
		if !ok {
			slog.Warn("unknown wake tone", "slot", n+1, "value", label)
			return
		}
		s.tone.Set(tone.Label())
		if err := settings.SetWakeTone(n, tone); err != nil {
			slog.Error("saving the wake tone failed", "slot", n+1, "err", err)
		}
	}
	s.effect.OnCommand = func(v string) {
		s.effect.Set(v)
		if err := settings.SetWakeEffect(n, v); err != nil {
			slog.Error("saving the wake effect failed", "slot", n+1, "err", err)
		}
	}
	return s
}

// restoreSlots publishes what the selected backend was last used with.
func (w *wakeControl) restoreSlots() {
	wake := settings.Get().Wake

	for i, s := range w.slots {
		saved := wake.Slot(i)
		s.threshold.Set(float32(saved.ThresholdOr(settings.DefaultThreshold)))
		s.tone.Set(saved.ToneOr(settings.DefaultTone).Label())
		s.effect.Set(saved.EffectOr(settings.DefaultEffect))
	}

	slog.Info("wake settings restored", "backend", wake.BackendOr(settings.DefaultBackend),
		"slots", len(w.slots))
}

func (w *wakeControl) entities() []esphome.Entity {
	ents := []esphome.Entity{w.backend}
	for _, s := range w.slots {
		ents = append(ents, s.threshold, s.tone, s.effect)
	}
	return ents
}

// Threshold is the score a slot's detection has to reach.
func (w *wakeControl) Threshold(slot int) float64 {
	if slot < 0 || slot >= len(w.slots) {
		return settings.DefaultThreshold
	}
	return float64(w.slots[slot].threshold.Get())
}

// Chime sounds a detection in whatever the slot is set to.
func (w *wakeControl) Chime(slot int) {
	if slot < 0 || slot >= len(w.slots) {
		return
	}
	tone, ok := settings.ByLabel(WakeTones(), w.slots[slot].tone.Get())
	if !ok {
		return
	}
	chime(w.speaker, wakeTones[tone])
}

// Flash shows a detection on the ring briefly, for one that starts no conversation.
func (w *wakeControl) Flash(slot int) {
	if effect := w.Effect(slot); effect != "" {
		w.ring.FlashEffect(effect, wakeFlash)
	}
}

// Hold starts the animation and leaves it running. A conversation uses this: the same animation runs
// from the wake word to the end of the reply, and the turn stops it.
func (w *wakeControl) Hold(slot int) {
	if effect := w.Effect(slot); effect != "" {
		w.ring.HoldEffect(effect)
	}
}

// Effect is the animation a slot plays, empty when it is turned off.
func (w *wakeControl) Effect(slot int) string {
	if slot < 0 || slot >= len(w.slots) {
		return ""
	}
	if e := w.slots[slot].effect.Get(); e != EffectNone {
		return e
	}
	return ""
}
