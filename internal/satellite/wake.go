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

// wakeControl is the wake words' settings and their feedback. Detection itself runs elsewhere and
// calls Detected.
//
// There is no switch for detection: a slot set to no wake word is off, and every slot off is
// detection off, which is the same thing Home Assistant's own wake word selects already say. A
// second control for it could only disagree with them.
type wakeControl struct {
	backend *esphome.Select
	slots   []wakeSlot

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
	delivery  *esphome.Select
	maxListen *esphome.Number
	maxThink  *esphome.Number
}

func newWakeControl(spk *speaker.Player, backends []settings.WakeBackend, slots int) *wakeControl {
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

// newSlot builds one slot's entities. The name leads with the assistant so that everything belonging
// to one of them reads together, beside Home Assistant's own Assistant and Wake word selects for the
// same slot. The object ids stay as they are: they set the entity id, and renaming those would orphan
// entities to no purpose.
func (w *wakeControl) newSlot(n int) wakeSlot {
	prefix := fmt.Sprintf("Assistant %d wake word", n+1)

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
		delivery: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("reply_delivery_%d", n+1),
				Name:     fmt.Sprintf("Assistant %d reply delivery", n+1),
				Icon:     "mdi:download-network",
				Category: esphome.CategoryConfig,
			},
			Options: settings.Labels(deliveries()),
		},
		maxListen: &esphome.Number{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("max_listen_%d", n+1),
				Name:     fmt.Sprintf("Assistant %d max listening time", n+1),
				Icon:     "mdi:timer-outline",
				Category: esphome.CategoryConfig,
			},
			Min: 5, Max: 60, Step: 1, Unit: "s",
			Mode: esphome.NumberBox,
		},
		maxThink: &esphome.Number{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("max_think_%d", n+1),
				Name:     fmt.Sprintf("Assistant %d max thinking time", n+1),
				Icon:     "mdi:timer-sand",
				Category: esphome.CategoryConfig,
			},
			Min: 5, Max: 300, Step: 5, Unit: "s",
			Mode: esphome.NumberBox,
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
	s.delivery.OnCommand = func(label string) {
		how, ok := settings.ByLabel(deliveries(), label)
		if !ok {
			slog.Warn("unknown reply delivery", "slot", n+1, "value", label)
			return
		}
		s.delivery.Set(how.Label())
		if err := settings.SetWakeDelivery(n, how); err != nil {
			slog.Error("saving the reply delivery failed", "slot", n+1, "err", err)
		}
		slog.Info("reply delivery", "slot", n+1, "using", how)
	}
	s.maxListen.OnCommand = func(v float32) {
		s.maxListen.Set(v)
		if err := settings.SetWakeMaxListen(n, int(v)); err != nil {
			slog.Error("saving the listening limit failed", "slot", n+1, "err", err)
		}
	}
	s.maxThink.OnCommand = func(v float32) {
		s.maxThink.Set(v)
		if err := settings.SetWakeMaxThink(n, int(v)); err != nil {
			slog.Error("saving the thinking limit failed", "slot", n+1, "err", err)
		}
	}
	return s
}

// deliveries is how a reply can arrive, in the order it is offered.
func deliveries() []settings.Delivery {
	return []settings.Delivery{settings.DeliveryWhole, settings.DeliveryStream}
}

// restoreSlots publishes what the selected backend was last used with.
func (w *wakeControl) restoreSlots() {
	wake := settings.Get().Wake

	for i, s := range w.slots {
		saved := wake.Slot(i)
		s.threshold.Set(float32(saved.ThresholdOr(settings.DefaultThreshold)))
		s.tone.Set(saved.ToneOr(settings.DefaultTone).Label())
		s.effect.Set(saved.EffectOr(settings.DefaultEffect))
		s.delivery.Set(saved.DeliveryOr(settings.DefaultDelivery).Label())
		s.maxListen.Set(float32(saved.MaxListenOr(settings.DefaultMaxListen)))
		s.maxThink.Set(float32(saved.MaxThinkOr(settings.DefaultMaxThink)))
	}

	slog.Info("wake settings restored", "backend", wake.BackendOr(settings.DefaultBackend),
		"slots", len(w.slots))
}

func (w *wakeControl) entities() []esphome.Entity {
	ents := []esphome.Entity{w.backend}
	for _, s := range w.slots {
		ents = append(ents, s.threshold, s.tone, s.effect, s.delivery, s.maxListen, s.maxThink)
	}
	return ents
}

// What a slot is set to is read from the settings, never back out of the entity. The entities are a
// view: a command writes the setting and then republishes it, so there is one direction of travel and
// labels only ever have to be resolved on the way in.

// saved is a slot's configuration.
func (w *wakeControl) saved(slot int) settings.WakeWord {
	return settings.Get().Wake.Slot(slot)
}

// Threshold is the score a slot's detection has to reach.
func (w *wakeControl) Threshold(slot int) float64 {
	return w.saved(slot).ThresholdOr(settings.DefaultThreshold)
}

// Chime sounds a detection in whatever the slot is set to.
func (w *wakeControl) Chime(slot int) {
	chime(w.speaker, wakeTones[w.saved(slot).ToneOr(settings.DefaultTone)])
}

// Delivery is how a slot's reply should reach the device.
func (w *wakeControl) Delivery(slot int) settings.Delivery {
	return w.saved(slot).DeliveryOr(settings.DefaultDelivery)
}

// MaxListen and MaxThink are how long a slot's turn may spend in each phase.
func (w *wakeControl) MaxListen(slot int) time.Duration {
	return time.Duration(w.saved(slot).MaxListenOr(settings.DefaultMaxListen)) * time.Second
}

func (w *wakeControl) MaxThink(slot int) time.Duration {
	return time.Duration(w.saved(slot).MaxThinkOr(settings.DefaultMaxThink)) * time.Second
}

// Effect is the animation a slot plays, empty when it is turned off. The conversation runs it: this
// only says which one, because which one is a setting.
func (w *wakeControl) Effect(slot int) string {
	if e := w.saved(slot).EffectOr(settings.DefaultEffect); e != EffectNone {
		return e
	}
	return ""
}
