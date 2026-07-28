package satellite

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/gpio"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/state"
)

// The two levels the mute LED has. The pin is a plain GPIO, so there is no range between them.
const (
	ledDim    = "Dim"
	ledBright = "Bright"
)

// muteSwitch is the microphone mute, drivable from Home Assistant and from the button on top of
// the device. gpio444 is a real cut rather than a software flag, so "muted" in Home Assistant
// means the microphones are disconnected.
type muteSwitch struct {
	sw         *esphome.Switch
	brightness *esphome.Select
	mute       *gpio.Mute
	led        *gpio.MuteLED
	speaker    *speaker.Player
}

func newMuteSwitch(m *gpio.Mute, led *gpio.MuteLED, spk *speaker.Player) *muteSwitch {
	s := &muteSwitch{
		sw: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "mic_mute",
				Name:     "Microphone mute",
				Icon:     "mdi:microphone-off",
			},
		},
		mute:    m,
		led:     led,
		speaker: spk,
	}
	s.sw.OnCommand = s.set

	// The mute line does not survive a reboot, so the saved value is applied rather than read.
	saved := state.Get().Settings
	if muted, err := m.Get(); err != nil {
		slog.Error("reading mute state failed", "err", err)
	} else if want := saved.Microphone.MutedOr(muted); want != muted {
		s.apply(want)
	} else {
		s.sw.Set(muted)
	}

	if led == nil {
		return s
	}
	s.brightness = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "mute_led_brightness",
			Name:     "Mute LED brightness",
			Icon:     "mdi:brightness-6",
			Category: esphome.CategoryConfig,
		},
		Options: []string{ledDim, ledBright},
	}
	s.brightness.OnCommand = s.setBrightness
	if bright, err := led.Bright(); err != nil {
		slog.Error("reading mute LED brightness failed", "err", err)
	} else if want := saved.Microphone.LEDBrightOr(bright); want != bright {
		s.applyBrightness(want)
	} else {
		s.brightness.Set(brightnessLabel(bright))
	}
	return s
}

// entities lists what the mute control exposes.
func (s *muteSwitch) entities() []esphome.Entity {
	if s.brightness == nil {
		return []esphome.Entity{s.sw}
	}
	return []esphome.Entity{s.sw, s.brightness}
}

func brightnessLabel(bright bool) string {
	if bright {
		return ledBright
	}
	return ledDim
}

func (s *muteSwitch) setBrightness(v string) {
	bright := v == ledBright
	s.applyBrightness(bright)
	if err := state.SetMicLEDBright(bright); err != nil {
		slog.Error("saving mute LED brightness failed", "err", err)
	}
}

func (s *muteSwitch) applyBrightness(bright bool) {
	if err := s.led.SetBright(bright); err != nil {
		slog.Error("setting mute LED brightness failed", "bright", bright, "err", err)
		return
	}
	if now, err := s.led.Bright(); err == nil {
		s.brightness.Set(brightnessLabel(now))
	}
}

// set drives the line and remembers the new state. apply is the quiet version, for start-up.
func (s *muteSwitch) set(muted bool) {
	s.apply(muted)
	if err := state.SetMicMuted(s.sw.Get()); err != nil {
		slog.Error("saving mute state failed", "err", err)
	}

	if s.sw.Get() {
		chime(s.speaker, toneMute)
		return
	}
	chime(s.speaker, toneUnmute)
}

// apply drives the line, then publishes what the hardware actually reads back.
func (s *muteSwitch) apply(muted bool) {
	if err := s.mute.Set(muted); err != nil {
		slog.Error("setting mute failed", "muted", muted, "err", err)
		return
	}
	s.publish()
}

func (s *muteSwitch) toggle() {
	muted, err := s.mute.Toggle()
	if err != nil {
		slog.Error("toggling mute failed", "err", err)
		return
	}
	s.sw.Set(muted)
	slog.Info("mute button", "muted", muted)

	if muted {
		chime(s.speaker, toneMute)
	} else {
		chime(s.speaker, toneUnmute)
	}
	if err := state.SetMicMuted(muted); err != nil {
		slog.Error("saving mute state failed", "err", err)
	}
}

func (s *muteSwitch) publish() {
	muted, err := s.mute.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}
	s.sw.Set(muted)
}
