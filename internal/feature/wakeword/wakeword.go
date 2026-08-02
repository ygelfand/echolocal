// Package wakeword is the wake word slots: how sensitive each one is, what it does when it fires, and
// how the reply that follows should arrive.
//
// Home Assistant pairs each of its wake word slots with its own pipeline, so everything here is per
// slot: two wake words can mean two different assistants, and they should not look or sound the same.
//
// Detection itself is not here — it runs against the microphones and calls in when it fires. There is
// no switch for it either: a slot with no wake word is off, and every slot off is detection off, which
// is what Home Assistant's own wake word selects already say. A second control could only disagree.
package wakeword

import (
	"fmt"
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/lib/hook"
)

func init() {
	component.Register(component.Device, Get(), component.Order(20))
}

// Slots is how many wake words Home Assistant offers at once, and so how many assistants there are to
// configure. Its own UI stops at two.
const Slots = 2

// Requested is a slot woken by hand rather than by hearing anything. What that means is the
// conversation's to decide, so this only says which slot.
var Requested hook.Hook[int]

type WakeWord struct {
	slots []slot
}

type slot struct {
	wake *esphome.Button

	threshold *esphome.Number
	tone      *esphome.Select
	effect    *esphome.Select
	delivery  *esphome.Select
	buffer    *esphome.Number
	followUp  *esphome.Number
	maxListen *esphome.Number
	maxThink  *esphome.Number
}

var (
	once   sync.Once
	shared *WakeWord
)

func Get() *WakeWord {
	once.Do(func() {
		shared = &WakeWord{}
		for i := range Slots {
			shared.slots = append(shared.slots, newSlot(i))
		}
	})
	return shared
}

// newSlot builds one slot's entities. The name leads with the assistant so that everything belonging
// to one of them reads together, beside Home Assistant's own Assistant and Wake word selects for the
// same slot.
func newSlot(n int) slot {
	prefix := fmt.Sprintf("Assistant %d wake word", n+1)

	s := slot{
		// Not diagnostic: waking the device by hand is something to do, not something to inspect.
		wake: &esphome.Button{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("wake_assistant_%d", n+1),
				Name:     fmt.Sprintf("Wake assistant %d", n+1),
				Icon:     "mdi:account-voice",
			},
			OnPress: func() { Requested.Emit(n) },
		},
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
			Options: config.Labels(speaker.WakeTones()),
		},
		effect: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("wake_effect_%d", n+1),
				Name:     prefix + " effect",
				Icon:     "mdi:animation",
				Category: esphome.CategoryConfig,
			},
			Options: append([]string{component.EffectNone}, led.EffectNames()...),
		},
		delivery: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("reply_delivery_%d", n+1),
				Name:     fmt.Sprintf("Assistant %d reply delivery", n+1),
				Icon:     "mdi:download-network",
				Category: esphome.CategoryConfig,
			},
			Options: config.Labels(deliveries()),
		},
		buffer: &esphome.Number{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("reply_buffer_%d", n+1),
				Name:     fmt.Sprintf("Assistant %d reply buffer", n+1),
				Icon:     "mdi:buffer",
				Category: esphome.CategoryConfig,
			},
			Min: 0, Max: 3000, Step: 50, Unit: "ms",
			Mode: esphome.NumberBox,
		},
		followUp: &esphome.Number{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("follow_up_%d", n+1),
				Name:     fmt.Sprintf("Assistant %d follow-up time", n+1),
				Icon:     "mdi:comment-question-outline",
				Category: esphome.CategoryConfig,
			},
			Min: 0, Max: 30, Step: 1, Unit: "s",
			Mode: esphome.NumberBox,
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
		if err := config.Set().Wake(n).Threshold(float64(v)); err != nil {
			slog.Error("saving the wake threshold failed", "slot", n+1, "err", err)
		}
		slog.Info("wake sensitivity", "slot", n+1, "cutoff", v)
	}
	s.tone.OnCommand = func(label string) {
		tone, ok := config.ByLabel(speaker.WakeTones(), label)
		if !ok {
			slog.Warn("unknown wake tone", "slot", n+1, "value", label)
			return
		}
		s.tone.Set(tone.Label())
		if err := config.Set().Wake(n).Tone(tone); err != nil {
			slog.Error("saving the wake tone failed", "slot", n+1, "err", err)
		}
	}
	component.BindEffect(s.effect, led.EffectNames(), nil,
		func(v string) error { return config.Set().Wake(n).Effect(v) })
	s.delivery.OnCommand = func(label string) {
		how, ok := config.ByLabel(deliveries(), label)
		if !ok {
			slog.Warn("unknown reply delivery", "slot", n+1, "value", label)
			return
		}
		s.delivery.Set(how.Label())
		if err := config.Set().Wake(n).Delivery(how); err != nil {
			slog.Error("saving the reply delivery failed", "slot", n+1, "err", err)
		}
		slog.Info("reply delivery", "slot", n+1, "using", how)
	}
	s.buffer.OnCommand = func(v float32) {
		s.buffer.Set(v)
		if err := config.Set().Wake(n).Buffer(int(v)); err != nil {
			slog.Error("saving the reply buffer failed", "slot", n+1, "err", err)
		}
	}
	s.followUp.OnCommand = func(v float32) {
		s.followUp.Set(v)
		if err := config.Set().Wake(n).FollowUp(int(v)); err != nil {
			slog.Error("saving the follow-up time failed", "slot", n+1, "err", err)
		}
	}
	s.maxListen.OnCommand = func(v float32) {
		s.maxListen.Set(v)
		if err := config.Set().Wake(n).MaxListen(int(v)); err != nil {
			slog.Error("saving the listening limit failed", "slot", n+1, "err", err)
		}
	}
	s.maxThink.OnCommand = func(v float32) {
		s.maxThink.Set(v)
		if err := config.Set().Wake(n).MaxThink(int(v)); err != nil {
			slog.Error("saving the thinking limit failed", "slot", n+1, "err", err)
		}
	}
	return s
}

// deliveries is how a reply can arrive, in the order it is offered.
func deliveries() []config.Delivery {
	return []config.Delivery{config.DeliveryWhole, config.DeliveryStream}
}

func (w *WakeWord) Name() string { return "wake word settings" }

func (w *WakeWord) Entities() []esphome.Entity {
	var ents []esphome.Entity
	for _, s := range w.slots {
		ents = append(ents, s.wake, s.threshold, s.tone, s.effect, s.delivery, s.buffer, s.followUp,
			s.maxListen, s.maxThink)
	}
	return ents
}

func (w *WakeWord) Restore(c config.Config) {
	for i, s := range w.slots {
		saved := c.Wake.Slot(i)
		s.threshold.Set(float32(saved.Threshold))
		s.tone.Set(saved.Tone.Label())

		component.RestoreEffect(s.effect, saved.Effect, nil,
			func(v string) error { return config.Set().Wake(i).Effect(v) })

		s.delivery.Set(saved.Delivery.Label())
		s.buffer.Set(float32(saved.Buffer))
		s.followUp.Set(float32(saved.FollowUp))
		s.maxListen.Set(float32(saved.MaxListen))
		s.maxThink.Set(float32(saved.MaxThink))
	}
	slog.Info("restored", "what", "wake word settings", "slots", len(w.slots))
}
