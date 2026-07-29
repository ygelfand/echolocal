package satellite

import (
	"log/slog"
	"slices"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/settings"
)

// ReactionNone is the option that leaves the ring alone.
const ReactionNone = "None"

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
			Options: append([]string{ReactionNone}, led.Names(led.KindRoom)...),
		},
		base: k.Ring.Base,
	}

	// Without a microphone there is no room to follow, so the only honest option is the one that does
	// nothing. The entity stays, because a device that hides controls when hardware fails is a device
	// nobody can tell has failed.
	if k.Mic != nil {
		r.level = k.Mic.Level
	} else {
		r.sel.Options = []string{ReactionNone}
	}
	if k.LEDs != nil {
		r.claim = k.LEDs.Claim(led.PriorityRoom)
	}

	r.sel.OnCommand = func(name string) { r.choose(name, true) }
	r.choose(settings.Get().Ring.ReactionOr(settings.DefaultReaction), false)
	return r
}

// choose starts following the room, or stops. save is false when restoring what was already stored.
func (r *roomReaction) choose(name string, save bool) {
	if name == "" {
		name = ReactionNone
	}

	// A name that is no longer offered — a stored one from a build that had it, or a hand-edited
	// settings file — falls back to doing nothing rather than to an effect that cannot run.
	if !slices.Contains(r.sel.Options, name) {
		slog.Warn("no such room reaction, leaving the ring alone", "reaction", name)
		name = ReactionNone
	}

	r.sel.Set(name)
	switch {
	case r.claim == nil:
	case name == ReactionNone:
		r.claim.Clear()
	default:
		r.claim.React(name, r.base(), r.level)
	}

	if !save {
		slog.Info("setting restored", "setting", r.sel.ObjectID, "using", name)
		return
	}
	stored := name
	if name == ReactionNone {
		stored = ""
	}
	if err := settings.SetRingReaction(stored); err != nil {
		slog.Error("saving the room reaction failed", "err", err)
	}
	slog.Info("setting changed", "setting", r.sel.ObjectID, "using", name)
}

// Recolour is called when the light's colour changes, so a reaction that inherits it follows along.
// The claim holds the colour it was given, so it has to be given the new one.
func (r *roomReaction) Recolour() {
	if r.claim == nil || r.sel.Get() == ReactionNone {
		return
	}
	r.claim.React(r.sel.Get(), r.base(), r.level)
}

func (r *roomReaction) entities() []esphome.Entity { return []esphome.Entity{r.sel} }
