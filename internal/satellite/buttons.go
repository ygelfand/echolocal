package satellite

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/buttons"
)

// Event types, as Home Assistant sees them. Repeats are not reported: a volume ramp would fill the
// logbook with dozens of entries for one press.
const (
	EventPress = "press"
	EventHold  = "hold"
)

// order is the buttons as Home Assistant lists them, since a map has none.
var order = []buttons.Name{buttons.Action, buttons.Mute, buttons.VolumeDown, buttons.VolumeUp}

// buttonEvents are the button entities. The device acts on a press itself; these are so Home Assistant
// can automate on one as well, including the ones the device does nothing with.
type buttonEvents struct {
	events map[buttons.Name]*esphome.Event
}

func newButtonEvents() *buttonEvents {
	b := &buttonEvents{events: map[buttons.Name]*esphome.Event{}}

	for _, name := range order {
		b.events[name] = &esphome.Event{
			Base: esphome.Base{
				ObjectID: "button_" + string(name),
				Name:     label(string(name)) + " button",
			},
			Types: []string{EventPress, EventHold},
		}
	}
	return b
}

func (b *buttonEvents) entities() []esphome.Entity {
	out := make([]esphome.Entity, 0, len(order))
	for _, name := range order {
		out = append(out, b.events[name])
	}
	return out
}

// OnButton is what the buttons do. The controller calls it; nothing here reads hardware.
//
// Volume and mute act on every tap and on every repeat, so a held volume button ramps. The action
// button is the only one where a hold means something different from a press, and what it means is
// the conversation's to decide.
func (s *Satellite) OnButton(e buttons.Event) {
	s.report(e)

	switch e.Name {
	case buttons.VolumeUp:
		if e.Kind != buttons.Hold {
			s.kit.Player.adjust(1)
		}
	case buttons.VolumeDown:
		if e.Kind != buttons.Hold {
			s.kit.Player.adjust(-1)
		}

	case buttons.Mute:
		switch e.Kind {
		case buttons.Tap:
			if s.mute != nil {
				s.mute.toggle()
			}
		case buttons.Hold:
			chime(s.kit.Sound, toneMuteHold)
		}

	case buttons.Action:
		switch e.Kind {
		case buttons.Tap:
			s.Action()
		case buttons.Hold:
			s.ActionHold()
		}
	}
}

// report tells Home Assistant, and logs. Both happen for every button whether the device does
// anything with it or not.
func (s *Satellite) report(e buttons.Event) {
	if e.Kind == buttons.Repeat {
		return
	}

	slog.Info("button", "button", e.Name, "kind", e.Kind)

	ev, ok := s.buttons.events[e.Name]
	if !ok {
		return
	}
	if e.Kind == buttons.Hold {
		ev.Trigger(EventHold)
		return
	}
	ev.Trigger(EventPress)
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
