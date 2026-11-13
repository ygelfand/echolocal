package wakeword

import (
	"fmt"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/assistant/wakesettings"
	"github.com/ygelfand/libcountertop/pkg/hook"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/tones"
	"github.com/ygelfand/echolocal/internal/hardware/led"
)

func init() {
	component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(20))
}

// Home Assistant's UI offers two wake word slots.
const Slots = 2

var Requested hook.Hook[int]

type WakeWord struct {
	*wakesettings.Settings
	effects []effects
}

type effects struct {
	wake     *esphome.Select
	thinking *esphome.Select
	replying *esphome.Select
}

var (
	once   sync.Once
	shared *WakeWord
)

func Get() *WakeWord {
	once.Do(func() {
		shared = &WakeWord{}
		for n := range Slots {
			shared.effects = append(shared.effects, newEffects(n))
		}
		shared.Settings = wakesettings.New(wakesettings.Options{
			Slots:    Slots,
			Screen:   component.Board().Has(board.Panel),
			DeviceID: component.AssistantDevice,
			Read:     func() schema.Wake { return config.Get().Wake },
			Write:    func(slot int) schema.WakeWriter { return config.Set().Wake(slot) },
			Wake:     Requested.Emit,
			Extras:   shared.extras,
			Tones:    tones.Get().Choices,
		})
		component.Settings.Add(shared.Controls())
		if component.Board().Has(board.Ring) {
			component.Settings.Add(shared.ring())
		}
		tones.Get().Changed.Listen(func(struct{}) {
			shared.Refresh(config.Get().Wake.Slots(Slots))
		})
	})
	return shared
}

func newEffects(n int) effects {
	// Home Assistant prefixes each entity name with its sub-device's name.
	on := component.AssistantDevice(n)

	e := effects{
		wake: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("wake_effect_%d", n+1),
				Name:     "Wake word effect",
				Icon:     "mdi:animation",
				Category: esphome.CategoryConfig,
				DeviceID: on,
			},
		},
		thinking: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("thinking_effect_%d", n+1),
				Name:     "Thinking effect",
				Icon:     "mdi:thought-bubble",
				Category: esphome.CategoryConfig,
				DeviceID: on,
			},
		},
		replying: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("replying_effect_%d", n+1),
				Name:     "Replying effect",
				Icon:     "mdi:message-text",
				Category: esphome.CategoryConfig,
				DeviceID: on,
			},
		},
	}

	component.BindEffect(e.wake, led.EffectNames(), nil,
		func(v string) error { return config.Set().Wake(n).Effect(v) })
	component.BindOverride(e.thinking, led.EffectNames(),
		func(v string) error { return config.Set().Wake(n).ThinkingEffect(v) })
	component.BindOverride(e.replying, led.EffectNames(),
		func(v string) error { return config.Set().Wake(n).ReplyingEffect(v) })
	return e
}

func (w *WakeWord) extras(slot int) []esphome.Entity {
	if !component.Board().Has(board.Ring) {
		return nil
	}
	e := w.effects[slot]
	return []esphome.Entity{e.wake, e.thinking, e.replying}
}

func (w *WakeWord) Restore(c config.Config) {
	w.Settings.Restore(c.Wake.Slots(Slots))

	for i, e := range w.effects {
		saved := c.Wake.Slot(i)
		component.RestoreEffect(e.wake, saved.Effect, nil,
			func(v string) error { return config.Set().Wake(i).Effect(v) })
		component.RestoreOverride(e.thinking, saved.ThinkingEffect,
			func(v string) error { return config.Set().Wake(i).ThinkingEffect(v) })
		component.RestoreOverride(e.replying, saved.ReplyingEffect,
			func(v string) error { return config.Set().Wake(i).ReplyingEffect(v) })
	}
}

func Threshold(slot int) float64 { return config.Get().Wake.Slot(slot).Threshold }
