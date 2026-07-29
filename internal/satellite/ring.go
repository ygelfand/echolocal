package satellite

import (
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/settings"
)

// ringLight maps Home Assistant's light entities onto the 12-segment ring: one light for the whole
// ring, plus a light per segment for people who want them.
//
// It is only the adapter. What the ring shows is decided by the driver, which resolves everything
// that wants it by priority; this holds the bottom layer, the appearance the ring returns to when
// nothing is happening. Nothing here cancels or restores anything, because nothing here can be
// interrupted: a conversation or a failure covers this layer rather than replacing it.
type ringLight struct {
	light *esphome.Light
	segs  []*esphome.Light
	base  *led.Claim

	mu    sync.Mutex
	frame []led.Color

	// followers want to know when the colour changes, because they show something in it: the room
	// reaction inherits the colour the same way an effect does, and its claim holds the colour it was
	// last given rather than looking it up.
	followers []func()
}

// OnColor registers something to tell when the light's colour changes.
func (r *ringLight) OnColor(f func()) { r.followers = append(r.followers, f) }

func newRingLight(k *kit) *ringLight {
	r := &ringLight{
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "ring", Name: "LED ring"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},

			// None comes first, because an effect list with no way out of it is a light that can be
			// animated and never made to hold still again.
			Effects: append([]string{EffectNone}, led.EffectNames()...),
		},
		base:  k.LEDs.Claim(led.PriorityBase),
		frame: make([]led.Color, led.Segments),
	}
	r.light.OnCommand = r.apply

	// Start from white at full brightness so the first command has something to turn on. Nothing is
	// shown yet: the claim stays empty until Home Assistant or a restore says otherwise, so the boot
	// animation is not competing with a resting color it would have to outrank.
	r.light.Set(esphome.LightState{
		ColorMode:  esphome.ColorModeRGB,
		Brightness: 1, Red: 1, Green: 1, Blue: 1,
	})

	for i := range led.Segments {
		seg := &esphome.Light{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("segment_%d", i+1),
				Name:     fmt.Sprintf("LED ring segment %d", i+1),
				// Twelve extra entities is a lot for anyone who just wants the ring.
				DisabledByDefault: true,
			},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},
		}
		seg.OnCommand = func(s esphome.LightState) { r.applySegment(i, seg, s) }
		r.segs = append(r.segs, seg)
	}
	return r
}

// entities lists everything the ring exposes.
func (r *ringLight) entities() []esphome.Entity {
	out := []esphome.Entity{r.light}
	for _, s := range r.segs {
		out = append(out, s)
	}
	return out
}

// apply takes a command from Home Assistant and makes it the ring's resting appearance.
func (r *ringLight) apply(s esphome.LightState) { r.set(s, true) }

// set is the one way an appearance is taken up, whether Home Assistant asked for it or it came back
// from the file. save is false for the second: nothing was chosen, so there is nothing new to remember,
// and a write at start-up is a write on the path most likely to be interrupted by another restart.
func (r *ringLight) set(s esphome.LightState, save bool) {
	s = usable(s)
	r.light.Set(s)
	r.show(s)

	for _, f := range r.followers {
		f()
	}
	if save {
		r.save()
	}
}

// save remembers the appearance, on every command.
func (r *ringLight) save() {
	s := r.light.Get()
	saved := settings.Light{
		On:         &s.On,
		Brightness: &s.Brightness,
		Red:        &s.Red,
		Green:      &s.Green,
		Blue:       &s.Blue,
	}

	// None is stored as no effect rather than as the name, so that the answer survives the option being
	// renamed and reads the same as never having chosen one.
	effect := ""
	if s.Effect != EffectNone {
		effect = s.Effect
	}
	saved.Effect = &effect

	if err := settings.SetRingLight(saved); err != nil {
		slog.Error("saving the ring light failed", "err", err)
	}
}

// restore puts back the appearance Home Assistant last set. Off unless it was on: a ring that lights
// the room by itself after a power cut is a surprise, which is why ESPHome makes that opt-in too.
func (r *ringLight) restore(saved settings.Ring) {
	l := saved.Light

	if !l.OnOr(false) {
		slog.Info("restored", "what", "ring light", "on", false, "from", from(l.Stored()))
		return
	}

	s := esphome.LightState{
		ColorMode:  esphome.ColorModeRGB,
		On:         true,
		Brightness: l.BrightnessOr(1),
		Red:        l.RedOr(1),
		Green:      l.GreenOr(1),
		Blue:       l.BlueOr(1),
		Effect:     effectOffered(r.light.Effects, l.EffectOr("")),
	}

	r.set(s, false)
	slog.Info("restored", "what", "ring light", "on", true,
		"brightness", s.Brightness, "effect", s.Effect, "from", from)
}

// effectOffered keeps a saved effect name only if this build still has it. An effect that is gone
// otherwise reaches RunEffect, fails, and leaves the ring dark with the reason in a log nobody reads.
func effectOffered(offered []string, name string) string {
	if name == "" || slices.Contains(offered, name) {
		return name
	}
	slog.Warn("no such effect, restoring a plain colour instead", "effect", name)
	return ""
}

// show puts the light's state on the base layer.
func (r *ringLight) show(s esphome.LightState) {
	if s.On && s.Effect != "" && s.Effect != EffectNone {
		r.base.Play(s.Effect, colorOf(s))
		return
	}

	c := led.Color{}
	if s.On {
		c = colorOf(s)
	}

	r.mu.Lock()
	for i := range r.frame {
		r.frame[i] = c
	}
	frame := append([]led.Color(nil), r.frame...)
	r.mu.Unlock()

	r.base.Paint(frame)
}

// applySegment paints one segment, leaving the others as they are.
func (r *ringLight) applySegment(i int, seg *esphome.Light, s esphome.LightState) {
	s = usable(s)
	seg.Set(s)

	r.mu.Lock()
	if s.On {
		r.frame[i] = colorOf(s)
	} else {
		r.frame[i] = led.Color{}
	}
	frame := append([]led.Color(nil), r.frame...)
	r.mu.Unlock()

	r.base.Paint(frame)
}

// Effect is the animation Home Assistant set on the ring, empty when it set none. A conversation runs
// the user's chosen wake effect, but the color it runs in comes from here.
func (r *ringLight) Base() led.Color { return colorOf(r.light.Get()) }

// colorOf is the light's color with its brightness folded in.
func colorOf(s esphome.LightState) led.Color {
	return led.Color{
		R: scale(s.Red, s.Brightness),
		G: scale(s.Green, s.Brightness),
		B: scale(s.Blue, s.Brightness),
	}
}

func scale(v, brightness float32) byte {
	return byte(math.Round(float64(v) * float64(brightness) * 255))
}

// usable fills in what a bare on command leaves out. Commands are partial and folded onto current
// state, so "on" with no brightness or color would otherwise light the ring black.
func usable(s esphome.LightState) esphome.LightState {
	if !s.On {
		return s
	}
	if s.Brightness == 0 {
		s.Brightness = 1
	}
	if s.Red == 0 && s.Green == 0 && s.Blue == 0 {
		s.Red, s.Green, s.Blue = 1, 1, 1
	}
	if s.ColorMode == 0 {
		s.ColorMode = esphome.ColorModeRGB
	}
	return s
}
