package satellite

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/gpio"
)

// muteSwitch is the microphone mute, drivable from Home Assistant and from the button on top of
// the device. gpio444 is a real cut rather than a software flag, so "muted" in Home Assistant
// means the microphones are disconnected.
type muteSwitch struct {
	sw     *esphome.Switch
	bright *esphome.Switch
	mute   *gpio.Mute
	led    *gpio.MuteLED
	log    *slog.Logger
}

func newMuteSwitch(m *gpio.Mute, led *gpio.MuteLED, log *slog.Logger) *muteSwitch {
	s := &muteSwitch{
		sw: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "mic_mute",
				Name:     "Microphone Mute",
				Icon:     "mdi:microphone-off",
			},
		},
		mute: m,
		led:  led,
		log:  log,
	}
	s.sw.OnCommand = s.set

	if muted, err := m.Get(); err != nil {
		log.Error("reading mute state failed", "err", err)
	} else {
		s.sw.Set(muted)
	}

	if led == nil {
		return s
	}
	s.bright = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "mute_led_bright",
			Name:     "Mute LED Bright",
			Icon:     "mdi:brightness-6",
			Category: esphome.CategoryConfig,
		},
	}
	s.bright.OnCommand = s.setBright
	if bright, err := led.Bright(); err != nil {
		log.Error("reading mute LED brightness failed", "err", err)
	} else {
		s.bright.Set(bright)
	}
	return s
}

// entities lists what the mute control exposes.
func (s *muteSwitch) entities() []esphome.Entity {
	if s.bright == nil {
		return []esphome.Entity{s.sw}
	}
	return []esphome.Entity{s.sw, s.bright}
}

func (s *muteSwitch) setBright(bright bool) {
	if err := s.led.SetBright(bright); err != nil {
		s.log.Error("setting mute LED brightness failed", "bright", bright, "err", err)
		return
	}
	if now, err := s.led.Bright(); err == nil {
		s.bright.Set(now)
	}
}

// set drives the line, then publishes what the hardware actually reads back.
func (s *muteSwitch) set(muted bool) {
	if err := s.mute.Set(muted); err != nil {
		s.log.Error("setting mute failed", "muted", muted, "err", err)
		return
	}
	s.publish()
}

func (s *muteSwitch) toggle() {
	muted, err := s.mute.Toggle()
	if err != nil {
		s.log.Error("toggling mute failed", "err", err)
		return
	}
	s.sw.Set(muted)
	s.log.Info("mute button", "muted", muted)
}

func (s *muteSwitch) publish() {
	muted, err := s.mute.Get()
	if err != nil {
		s.log.Error("reading mute state failed", "err", err)
		return
	}
	s.sw.Set(muted)
}
