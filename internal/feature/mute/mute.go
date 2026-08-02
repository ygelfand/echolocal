// Package mute is the microphone mute: the switch in Home Assistant, the button on top of the
// device, and what the ring shows while the microphones are cut.
//
// The pin is a real cut rather than a software flag, so muted here means the microphones are
// disconnected. Home Assistant asks for a state, the button asks for the other one, and start-up
// asks for whatever was stored — they move the line differently and then want exactly the same
// things to follow, so they share settled.
package mute

import (
	"log/slog"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/component"
	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/buttons"
	"github.com/ygelfand/echolocal/internal/hardware/gpio"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

func init() {
	component.Register(component.Device, Get(), component.Order(30))
}

// The two levels the mute LED has. The pin is a plain GPIO, so there is nothing between them.
const (
	dim    = "Dim"
	bright = "Bright"
)

// mutedColor is what an inheriting animation runs in while the microphones are cut. Red, like the
// button's own LED and like a failure: the device cannot hear, which is closer to being broken than
// to being a colour someone chose.
var mutedColor = led.Color{R: 0xC0, G: 0x00, B: 0x00}

type Mute struct {
	sw         *esphome.Switch
	brightness *esphome.Select
	line       *gpio.Mute
	led        *gpio.MuteLED

	// ring is the animation to show while the microphones are cut, and claim is where it goes. The
	// select lives here rather than with the other settings because choosing one has to take effect
	// immediately: being muted has no next occurrence to wait for, it is already happening.
	ring  *esphome.Select
	claim *led.Claim
}

var (
	once   sync.Once
	shared *Mute
)

func Get() *Mute {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Mute {
	m := &Mute{
		sw: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "mic_mute",
				Name:     "Microphone mute",
				Icon:     "mdi:microphone-off",
			},
		},
		claim: led.Get().Claim(led.PriorityMute),
		ring: &esphome.Select{
			Base: esphome.Base{
				ObjectID: "ring_muted",
				Name:     "Ring while muted",
				Icon:     "mdi:microphone-off",
				Category: esphome.CategoryConfig,
			},
		},
	}
	m.sw.OnCommand = m.Set
	component.BindEffect(m.ring, led.EffectNames(), m.show, config.Set().Ring().Muted)

	// The entities exist whether or not the pins do. A device that hides controls when its hardware
	// fails is a device nobody can tell has failed, and the log says which of the two went missing.
	m.brightness = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "mute_led_brightness",
			Name:     "Mute LED brightness",
			Icon:     "mdi:brightness-6",
			Category: esphome.CategoryConfig,
		},
		Options: []string{dim, bright},
	}
	m.brightness.OnCommand = m.setBrightness

	var err error
	if m.line, err = gpio.Microphone(); err != nil {
		slog.Error("mute unavailable", "err", err)
	}
	if m.led, err = gpio.LED(); err != nil {
		slog.Error("mute LED unavailable", "err", err)
	}

	buttons.Get().Events.Listen(m.pressed)
	return m
}

func (m *Mute) Name() string { return "microphone mute" }

func (m *Mute) Entities() []esphome.Entity {
	return []esphome.Entity{m.sw, m.ring, m.brightness}
}

// Muted reports whether the line is cut, which is what a turn has to check before opening the
// microphones.
func (m *Mute) Muted() (bool, error) {
	if m.line == nil {
		return false, nil
	}
	return m.line.Get()
}

// Restore cuts the microphones if they were cut when the device was last on. The line does not
// survive a reboot, so what was stored is applied rather than read — and it is the line that
// decides, so a device whose GPIO cannot be reached comes up live and says so rather than claiming
// to be muted.
func (m *Mute) Restore(c config.Config) {
	if m.line == nil {
		return
	}
	was, err := m.line.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}

	// What to show while cut, before cutting, so the ring is right the first time settled looks.
	component.RestoreEffect(m.ring, c.Ring.Muted, nil, config.Set().Ring().Muted)

	want := c.Microphone.Muted
	if want != was {
		if err := m.line.Set(want); err != nil {
			slog.Error("setting mute failed", "muted", want, "err", err)
		}
	}
	m.settled(false)
	slog.Info("restored", "what", "microphone mute", "muted", m.sw.Get(), "asked", want)

	m.applyBrightness(c.Microphone.LEDBright)
	slog.Info("restored", "what", m.brightness.ObjectID, "using", label(c.Microphone.LEDBright))
}

// Set is the switch in Home Assistant.
func (m *Mute) Set(muted bool) {
	if m.line == nil {
		return
	}
	if err := m.line.Set(muted); err != nil {
		slog.Error("setting mute failed", "muted", muted, "err", err)
		return
	}
	m.settled(true)
}

// Toggle is the button on top of the device.
func (m *Mute) Toggle() {
	if m.line == nil {
		return
	}
	if _, err := m.line.Toggle(); err != nil {
		slog.Error("toggling mute failed", "err", err)
		return
	}
	m.settled(true)
}

// pressed is the mute button. A hold only sounds: nothing is bound to it, and the tone says the
// press was heard.
func (m *Mute) pressed(e buttons.Event) {
	if e.Name != buttons.Mute {
		return
	}
	switch e.Kind {
	case buttons.Tap:
		m.Toggle()
	case buttons.Hold:
		speaker.Sound().Chime(speaker.ToneMuteHold)
	}
}

// settled publishes what the line now reads — not what was asked for, so a line that did not move
// says so — and shows it on the ring. asked is false at start-up, where nobody asked.
func (m *Mute) settled(asked bool) {
	muted, err := m.line.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}

	m.sw.Set(muted)
	m.show(component.ChosenEffect(m.ring))

	if !asked {
		return
	}

	if err := config.Set().Microphone().Muted(muted); err != nil {
		slog.Error("saving mute state failed", "err", err)
	}
	slog.Info("microphone mute", "muted", muted)

	if muted {
		speaker.Sound().Chime(speaker.ToneMute)
		return
	}
	speaker.Sound().Chime(speaker.ToneUnmute)
}

// show puts an animation on the ring for as long as the microphones are cut, or takes it off. Unlike
// a failure this has no duration of its own, so the claim is held and cleared rather than timed.
//
// It takes the name rather than reading the setting, because it is called both when the mute state
// changes and when the choice does, and on that second path the setting has not been written yet.
func (m *Mute) show(name string) {
	if name == "" || !m.sw.Get() {
		m.claim.Clear()
		return
	}
	m.claim.Play(name, mutedColor)
}

func (m *Mute) setBrightness(v string) {
	on := v == bright
	m.applyBrightness(on)
	if err := config.Set().Microphone().LEDBright(on); err != nil {
		slog.Error("saving mute LED brightness failed", "err", err)
	}
}

func (m *Mute) applyBrightness(on bool) {
	if m.led == nil {
		return
	}
	if err := m.led.SetBright(on); err != nil {
		slog.Error("setting mute LED brightness failed", "bright", on, "err", err)
		return
	}
	if now, err := m.led.Bright(); err == nil {
		m.brightness.Set(label(now))
	}
}

func label(on bool) string {
	if on {
		return bright
	}
	return dim
}
