package satellite

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/settings"
)

// roomReaction is the ring following the room: chosen once and then simply on, which is why it is not
// in the light's effect list. An effect there is an appearance Home Assistant set and can set again;
// this is a standing instruction to show what the microphone hears until told otherwise.
//
// It holds its own claim above the light's resting colour, so choosing None reveals whatever the light
// was set to without this having to remember or restore anything. A conversation, a volume change or a
// failure covers it and gives it back on its own.
type roomReaction struct {
	sel   *esphome.Select
	claim *led.Claim

	// level is where the room comes from, and base what colour to show it in. Both are read at the
	// moment a frame is drawn rather than captured, so leveling and the light's colour stay live.
	level led.Level
	base  func() led.Color
}

func newRoomReaction(k *kit) *roomReaction {
	r := &roomReaction{
		sel: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "room_reaction",
				Name:     "Ring follows the room",
				Icon:     "mdi:waveform",
				Category: esphome.CategoryConfig,
			},
		},
		base: k.Ring.Base,
	}

	// Without a microphone there is no room to follow, so the only honest option is the one that does
	// nothing. The entity stays, because a device that hides controls when hardware fails is a device
	// nobody can tell has failed.
	choices := led.Names(led.KindRoom)
	if k.Mic != nil {
		r.level = k.Mic.Level
	} else {
		choices = nil
	}
	if k.LEDs != nil {
		r.claim = k.LEDs.Claim(led.PriorityRoom)
	}

	bindEffect(r.sel, choices, r.show, settings.SetRingReaction)
	return r
}

// restore starts following the room again, if that is what was chosen.
func (r *roomReaction) restore(saved settings.Ring) {
	restoreEffect(r.sel, saved.ReactionOr(settings.DefaultReaction), r.show,
		settings.SetRingReaction, saved.Reaction != nil)
}

// show puts the chosen reaction on the ring, or takes it off. Empty is None: the claim is cleared
// rather than released, so whatever the light was set to comes back on its own.
func (r *roomReaction) show(name string) {
	switch {
	case r.claim == nil:
	case name == "":
		r.claim.Clear()
	default:
		r.claim.React(name, r.base(), r.level)
	}
}

// Recolour is called when the light's colour changes, so a reaction that inherits it follows along.
// The claim holds the colour it was given, so it has to be given the new one.
func (r *roomReaction) Recolour() {
	r.show(settings.Get().Ring.ReactionOr(settings.DefaultReaction))
}

func (r *roomReaction) entities() []esphome.Entity { return []esphome.Entity{r.sel} }
