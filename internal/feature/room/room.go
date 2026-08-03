// Package room is the ring following the room: chosen once and then simply on, which is why it is
// not in the light's effect list. An effect there is an appearance Home Assistant set and can set
// again; this is a standing instruction to show what the microphone hears until told otherwise.
//
// It holds its own claim above the light's resting colour, so choosing None reveals whatever the
// light was set to without this having to remember or restore anything. A conversation, a volume
// change or a failure covers it and gives it back on its own.
package room

import (
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/feature/light"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/mic"
)

// After the light, whose colour it inherits.
func init() {
	component.Register(component.Device, Get(), component.Order(20))
}

type Reaction struct {
	sel   *esphome.Select
	claim *led.Claim

	// room is what the effect may ask about the room, and base what colour to show it in. Both are
	// read at the moment a frame is drawn rather than captured, so leveling and the light's colour
	// stay live.
	room led.Room
	base func() led.Color
}

var (
	once   sync.Once
	shared *Reaction
)

func Get() *Reaction {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Reaction {
	r := &Reaction{
		sel: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "room_reaction",
				DeviceID: component.DeviceRing,
				Name:     "Ring follows the room",
				Icon:     "mdi:waveform",
				Category: esphome.CategoryConfig,
			},
		},
		base:  light.Get().Base,
		claim: led.Get().Claim(led.PriorityRoom),
	}

	source := mic.Get()
	r.room = led.Room{Level: source.Level, Facing: source.Facing}

	component.BindEffect(r.sel, led.Names(led.KindRoom), r.show, config.Set().Ring().Reaction)

	// The light's colour is inherited, and the claim holds the colour it was given rather than
	// looking it up, so a change has to be handed over.
	light.Get().OnColor(r.recolour)
	return r
}

func (r *Reaction) Name() string { return "room reaction" }

func (r *Reaction) Entities() []esphome.Entity { return []esphome.Entity{r.sel} }

// Restore starts following the room again, if that is what was chosen.
func (r *Reaction) Restore(c config.Config) {
	component.RestoreEffect(r.sel, c.Ring.Reaction, r.show, config.Set().Ring().Reaction)
}

// show puts the chosen reaction on the ring, or takes it off. Empty is None: the claim is cleared
// rather than released, so whatever the light was set to comes back on its own.
func (r *Reaction) show(name string) {
	if name == "" {
		r.claim.Clear()
		return
	}
	r.claim.React(name, r.base(), r.room)
}

func (r *Reaction) recolour() { r.show(config.Get().Ring.Reaction) }
