// Package light is the ring as Home Assistant sees it: one light for the whole ring, plus a light
// per segment for people who want them.
//
// It is only the adapter. What the ring shows is decided by the driver, which resolves everything
// that wants it by priority; this holds the bottom layer, the appearance the ring returns to when
// nothing is happening. Nothing here cancels or restores anything, because nothing here can be
// interrupted: a conversation or a failure covers this layer rather than replacing it.
package light

import (
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/led"
)

func init() {
	component.Register(component.Device, Get(), component.Order(10))
}

type Light struct {
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

var (
	once   sync.Once
	shared *Light
)

func Get() *Light {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Light {
	l := &Light{
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "ring", Name: "LED ring", DeviceID: component.DeviceRing},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},

			// None comes first, because an effect list with no way out of it is a light that can be
			// animated and never made to hold still again.
			Effects: append([]string{component.EffectNone}, led.EffectNames()...),
		},
		base:  led.Get().Claim(led.PriorityBase),
		frame: make([]led.Color, led.Segments),
	}
	l.light.OnCommand = func(s esphome.LightState) { l.set(s, true) }

	// Start from white at full brightness so the first command has something to turn on. Nothing is
	// shown yet: the claim stays empty until Home Assistant or a restore says otherwise, so the boot
	// animation is not competing with a resting colour it would have to outrank.
	l.light.Set(esphome.LightState{
		ColorMode:  esphome.ColorModeRGB,
		Brightness: 1, Red: 1, Green: 1, Blue: 1,
	})

	for i := range led.Segments {
		seg := &esphome.Light{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("segment_%d", i+1),
				Name:     fmt.Sprintf("LED ring segment %d", i+1),
				DeviceID: component.DeviceRing,
				// Twelve extra entities is a lot for anyone who just wants the ring.
				DisabledByDefault: true,
			},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},
		}
		seg.OnCommand = func(s esphome.LightState) { l.applySegment(seg, s) }
		l.segs = append(l.segs, seg)
	}
	return l
}

func (l *Light) Name() string { return "ring light" }

func (l *Light) Entities() []esphome.Entity {
	out := []esphome.Entity{l.light}
	for _, s := range l.segs {
		out = append(out, s)
	}
	return out
}

// OnColor registers something to tell when the light's colour changes.
func (l *Light) OnColor(f func()) { l.followers = append(l.followers, f) }

// Base is the colour Home Assistant set, which is what an effect someone else runs takes its colour
// from.
func (l *Light) Base() led.Color { return colorOf(l.light.Get()) }

// Restore puts back the appearance Home Assistant last set. Off unless it was on: a ring that lights
// the room by itself after a power cut is a surprise, which is why ESPHome makes that opt-in too.
func (l *Light) Restore(c config.Config) {
	saved := c.Ring.Light

	if !saved.On {
		slog.Info("restored", "what", "ring light", "on", false)
		return
	}

	s := esphome.LightState{
		ColorMode:  esphome.ColorModeRGB,
		On:         true,
		Brightness: saved.Brightness,
		Red:        saved.Red,
		Green:      saved.Green,
		Blue:       saved.Blue,
		Effect:     offered(l.light.Effects, saved.Effect),
	}

	l.set(s, false)
	slog.Info("restored", "what", "ring light", "on", true,
		"brightness", s.Brightness, "effect", s.Effect)
}

// set is the one way an appearance is taken up, whether Home Assistant asked for it or it came back
// from the file. save is false for the second: nothing was chosen, so there is nothing new to
// remember, and a write at start-up is a write on the path most likely to be interrupted by another
// restart.
func (l *Light) set(s esphome.LightState, save bool) {
	s = usable(s)
	l.light.Set(s)
	l.show()

	for _, f := range l.followers {
		f()
	}
	if save {
		l.save()
	}
}

func (l *Light) save() {
	s := l.light.Get()

	// None is stored as no effect rather than as the name, so the answer survives the option being
	// renamed and reads the same as never having chosen one.
	effect := ""
	if s.Effect != component.EffectNone {
		effect = s.Effect
	}

	saved := config.Light{
		On:         s.On,
		Brightness: s.Brightness,
		Red:        s.Red,
		Green:      s.Green,
		Blue:       s.Blue,
		Effect:     effect,
	}
	if err := config.Set().Ring().Light(saved); err != nil {
		slog.Error("saving the ring light failed", "err", err)
	}
}

// show works out the whole appearance and puts it on the base layer. Everything that changes any part of
// it comes through here, so the entities are the only state: the ring's, and each segment's.
//
// An effect owns all twelve segments — it is one animation across the ring, not twelve — so while one is
// running the overrides are remembered and not shown. They appear when the effect is turned off.
func (l *Light) show() {
	s := l.light.Get()

	if s.On && s.Effect != "" && s.Effect != component.EffectNone {
		l.base.Play(s.Effect, colorOf(s))
		return
	}

	c := led.Color{}
	if s.On {
		c = colorOf(s)
	}

	l.mu.Lock()
	for i, seg := range l.segs {
		// A segment somebody set by hand keeps what it was given: asking for one red means it stays red
		// when the rest is turned down. Off is how that is given up, which is why it is not black.
		if o := seg.Get(); o.On {
			l.frame[i] = colorOf(o)
			continue
		}
		l.frame[i] = c
	}
	frame := slices.Clone(l.frame)
	l.mu.Unlock()

	l.base.Paint(frame)
}

// applySegment takes one segment's override and shows the ring again with it.
func (l *Light) applySegment(seg *esphome.Light, s esphome.LightState) {
	seg.Set(usable(s))
	l.show()
}

// offered keeps a saved effect name only if this build still has it. One that is gone otherwise
// reaches RunEffect, fails, and leaves the ring dark with the reason in a log nobody reads.
func offered(have []string, name string) string {
	if name == "" || slices.Contains(have, name) {
		return name
	}
	slog.Warn("no such effect, restoring a plain colour instead", "effect", name)
	return ""
}

// colorOf is the light's colour with its brightness folded in.
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
// state, so "on" with no brightness or colour would otherwise light the ring black.
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
