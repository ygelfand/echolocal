package component

import (
	"log/slog"
	"slices"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/config"
)

// Wiring a Home Assistant select to a setting, which several features do the same way: one to a
// closed set of values, one to the catalogue of ring animations.

// Bind wires a select to an enumerated setting: the options are the values' labels, and choosing one
// applies it and stores whatever the device settled on.
//
// apply returns what it settled on, so a value this build cannot do falls back visibly rather than
// leaving Home Assistant showing something that is not running.
func Bind[T config.Labelled](sel *esphome.Select, values []T, apply func(T) T, save func(T) error) {
	sel.Options = config.Labels(values)

	sel.OnCommand = func(label string) {
		want, ok := config.ByLabel(values, label)
		if !ok {
			slog.Warn("unknown option", "setting", sel.ObjectID, "value", label)
			return
		}

		settled := apply(want)
		sel.Set(settled.Label())
		if err := save(settled); err != nil {
			slog.Error("saving a setting failed", "setting", sel.ObjectID, "err", err)
		}
		slog.Info("setting changed", "setting", sel.ObjectID, "using", settled)
	}
}

// Restore applies a saved value and publishes what the device settled on, which is not always the
// same thing.
func Restore[T config.Labelled](sel *esphome.Select, saved T, apply func(T) T) {
	settled := apply(saved)
	sel.Set(settled.Label())
	slog.Info("restored", "what", sel.ObjectID, "using", settled, "asked", saved)
}

// EffectNone is the way out of an effect list: an animation that cannot be turned off is a ring that
// cannot be made to hold still.
const EffectNone = "None"

// SettleEffect puts a name on a select and reports what it settled on, empty for None.
//
// A name this build does not have — stored by one that did, or hand-edited — settles to None rather
// than being offered: unsettled, it reaches RunEffect, fails, and leaves the ring dark for as long
// as whatever wanted it holds the claim.
func SettleEffect(sel *esphome.Select, name string) string {
	if name == "" {
		name = EffectNone
	}
	if !slices.Contains(sel.Options, name) {
		slog.Warn("no such effect, showing nothing instead", "setting", sel.ObjectID, "effect", name)
		name = EffectNone
	}

	sel.Set(name)
	if name == EffectNone {
		return ""
	}
	return name
}

// BindEffect offers the catalogue on a select and saves what is chosen. apply is for a choice
// something has to be told about as it happens, such as the room reaction, which holds a claim; it
// is nil for the rest, where whoever shows the animation looks the name up when the moment comes.
func BindEffect(sel *esphome.Select, choices []string, apply func(string), save func(string) error) {
	sel.Options = append([]string{EffectNone}, choices...)

	sel.OnCommand = func(chosen string) {
		settled := SettleEffect(sel, chosen)
		if apply != nil {
			apply(settled)
		}
		if err := save(settled); err != nil {
			slog.Error("saving a setting failed", "setting", sel.ObjectID, "err", err)
		}
		slog.Info("setting changed", "setting", sel.ObjectID, "using", sel.Get())
	}
}

// RestoreEffect puts a saved name back, correcting one this build does not have.
//
// A correction is written back, because what reads these when the moment comes is the setting rather
// than the select — a failure indication has no way to reach the entity. Leaving the two disagreeing
// is how a stored name that cannot run keeps being tried.
func RestoreEffect(sel *esphome.Select, saved string, apply func(string), save func(string) error) {
	settled := SettleEffect(sel, saved)
	if apply != nil {
		apply(settled)
	}
	if settled != saved {
		if err := save(settled); err != nil {
			slog.Error("saving a corrected setting failed", "setting", sel.ObjectID, "err", err)
		}
	}
	slog.Info("restored", "what", sel.ObjectID, "using", sel.Get())
}

// ChosenEffect is what a select settled on, empty when it says None.
func ChosenEffect(sel *esphome.Select) string {
	if name := sel.Get(); name != EffectNone {
		return name
	}
	return ""
}
