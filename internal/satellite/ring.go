package satellite

import (
	"fmt"
	"math"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/led"
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
}

func newRingLight(d *led.Driver) *ringLight {
	r := &ringLight{
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "ring", Name: "LED ring"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},
			Effects:             led.EffectNames(),
		},
		base:  d.Claim(led.PriorityBase),
		frame: make([]led.Color, led.Segments),
	}
	r.light.OnCommand = r.apply

	// Start from white at full brightness so the first command has something to turn on. Nothing is
	// shown yet: the claim stays empty until Home Assistant or a restore says otherwise, so the boot
	// animation is not competing with a resting colour it would have to outrank.
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
func (r *ringLight) apply(s esphome.LightState) {
	s = usable(s)
	r.light.Set(s)
	r.show(s)
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
// the user's chosen wake effect, but the colour it runs in comes from here.
func (r *ringLight) Base() led.Color { return colorOf(r.light.Get()) }

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
