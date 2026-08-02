// Package buttons is the four buttons as Home Assistant sees them.
//
// The device acts on a press itself; these are so an automation can act on one as well, including
// the buttons the device does nothing with.
package buttons

import (
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/hardware/buttons"
)

func init() {
	component.Register(component.Device, Get())
}

// Event types, as Home Assistant sees them. Repeats are not reported: a volume ramp would fill the
// logbook with dozens of entries for one press.
const (
	Press = "press"
	Hold  = "hold"
)

// order is the buttons as Home Assistant lists them, since a map has none.
var order = []buttons.Name{buttons.Action, buttons.Mute, buttons.VolumeDown, buttons.VolumeUp}

type Events struct {
	events map[buttons.Name]*esphome.Event
}

var (
	once   sync.Once
	shared *Events
)

// Get is the button entities, listening to the hardware from the moment it is built.
func Get() *Events {
	once.Do(func() {
		shared = &Events{events: map[buttons.Name]*esphome.Event{}}
		for _, name := range order {
			shared.events[name] = &esphome.Event{
				Base: esphome.Base{
					ObjectID: "button_" + string(name),
					Name:     label(string(name)) + " button",
				},
				Types: []string{Press, Hold},
			}
		}
		buttons.Get().Events.Listen(shared.report)
	})
	return shared
}

func (e *Events) Name() string { return "button events" }

func (e *Events) Entities() []esphome.Entity {
	out := make([]esphome.Entity, 0, len(order))
	for _, name := range order {
		out = append(out, e.events[name])
	}
	return out
}

// report tells Home Assistant, and logs. Both happen for every button whether the device does
// anything with it or not.
func (e *Events) report(ev buttons.Event) {
	if ev.Kind == buttons.Repeat {
		return
	}
	slog.Info("button", "button", ev.Name, "kind", ev.Kind)

	entity, ok := e.events[ev.Name]
	if !ok {
		return
	}
	if ev.Kind == buttons.Hold {
		entity.Trigger(Hold)
		return
	}
	entity.Trigger(Press)
}

func label(name string) string {
	out := []rune(name)
	for i, r := range out {
		if r == '_' {
			out[i] = ' '
		}
	}
	out[0] -= 32
	return string(out)
}
