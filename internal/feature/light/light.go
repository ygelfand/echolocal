package light

import (
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"

	"github.com/ygelfand/echolocal/internal/board"
	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/led"
)

func init() {
	component.Register(
		sharedcomponent.Device,
		Get,
		sharedcomponent.Order(10),
		component.Needs(board.Ring),
	)
}

type Light struct {
	light *esphome.Light
	segs  []*esphome.Light
	base  *led.Claim

	mu    sync.Mutex
	frame []led.Color

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
			Base: esphome.Base{
				ObjectID: "ring",
				Name:     "LED ring",
				DeviceID: component.DeviceRing,
				Category: esphome.CategoryConfig,
			},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeRGB},

			Effects: append([]string{component.EffectNone}, led.EffectNames()...),
		},
		base:  led.Get().Claim(led.PriorityBase),
		frame: make([]led.Color, led.Segments),
	}
	l.light.OnCommand = func(s esphome.LightState) { l.set(s, true) }

	l.light.Set(esphome.LightState{
		ColorMode:  esphome.ColorModeRGB,
		Brightness: 1, Red: 1, Green: 1, Blue: 1,
	})

	for i := range led.Segments {
		seg := &esphome.Light{
			Base: esphome.Base{
				ObjectID:          fmt.Sprintf("segment_%d", i+1),
				Name:              fmt.Sprintf("LED ring segment %d", i+1),
				DeviceID:          component.DeviceRing,
				DisabledByDefault: true,
				Category:          esphome.CategoryConfig,
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

func (l *Light) OnColor(f func()) { l.followers = append(l.followers, f) }

func (l *Light) Base() led.Color { return colorOf(l.light.Get()) }

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

func (l *Light) applySegment(seg *esphome.Light, s esphome.LightState) {
	seg.Set(usable(s))
	l.show()
}

func offered(have []string, name string) string {
	if name == "" || slices.Contains(have, name) {
		return name
	}
	slog.Warn("no such effect, restoring a plain colour instead", "effect", name)
	return ""
}

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
