package wakeword

import (
	"time"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

// What a slot is set to is read from the config, never back out of the entity. The entities are a
// view: a command writes the setting and then republishes it, so there is one direction of travel and
// labels only ever have to be resolved on the way in.
func saved(slot int) config.WakeWord { return config.Get().Wake.Slot(slot) }

// Threshold is the score a slot's detection has to reach.
func Threshold(slot int) float64 { return saved(slot).Threshold }

// Chime sounds a detection in whatever the slot is set to.
func Chime(slot int) { speaker.Sound().Chime(speaker.WakeTone(saved(slot).Tone)) }

// Tones reports whether the slot makes a sound when it fires.
func Tones(slot int) bool { return saved(slot).Tone != config.ToneNone }

// ChimeLength is how long that sound lasts.
func ChimeLength(slot int) time.Duration {
	return speaker.Length(speaker.WakeTone(saved(slot).Tone))
}

// Delivery is how a slot's reply should reach the device.
func Delivery(slot int) config.Delivery { return saved(slot).Delivery }

// Buffer is how much of a streamed reply to collect before playing any of it.
func Buffer(slot int) time.Duration {
	return time.Duration(saved(slot).Buffer) * time.Millisecond
}

// FollowUp is how long a turn opened without a wake word listens for, zero when only Home Assistant
// may ask for one.
func FollowUp(slot int) time.Duration {
	return time.Duration(saved(slot).FollowUp) * time.Second
}

// MaxListen and MaxThink are how long a slot's turn may spend in each phase.
func MaxListen(slot int) time.Duration {
	return time.Duration(saved(slot).MaxListen) * time.Second
}

func MaxThink(slot int) time.Duration {
	return time.Duration(saved(slot).MaxThink) * time.Second
}

// Effect is the animation a slot plays, empty when it is turned off. The conversation runs it: this
// only says which one, because which one is a setting.
func Effect(slot int) string {
	if e := saved(slot).Effect; e != component.EffectNone {
		return e
	}
	return ""
}
