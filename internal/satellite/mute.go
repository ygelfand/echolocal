package satellite

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/gpio"
	"github.com/ygelfand/echolocal/internal/led"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
)

// The two levels the mute LED has. The pin is a plain GPIO, so there is no range between them.
const (
	ledDim    = "Dim"
	ledBright = "Bright"
)

// mutedColor is what an inheriting animation runs in while the microphones are cut. Red, like the
// button's own LED and like a failure: the device cannot hear, which is closer to being broken than to
// being a colour someone chose.
var mutedColor = led.Color{R: 0xC0, G: 0x00, B: 0x00}

// muteSwitch is the microphone mute, drivable from Home Assistant and from the button on top of
// the device. gpio444 is a real cut rather than a software flag, so "muted" in Home Assistant
// means the microphones are disconnected.
type muteSwitch struct {
	sw         *esphome.Switch
	brightness *esphome.Select
	mute       *gpio.Mute
	led        *gpio.MuteLED
	sound      *speaker.Driver

	// ring is the animation to show while the microphones are cut, and claim is where it goes. The
	// select lives here rather than with the other settings because choosing one has to take effect
	// immediately: being muted has no next occurrence to wait for, it is already happening.
	ring  *esphome.Select
	claim *led.Claim
}

func newMuteSwitch(k *kit) *muteSwitch {
	m, muteLED, sound := k.Mute, k.MuteLED, k.Sound
	s := &muteSwitch{
		sw: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "mic_mute",
				Name:     "Microphone mute",
				Icon:     "mdi:microphone-off",
			},
		},
		mute:  m,
		led:   muteLED,
		sound: sound,
	}
	if k.LEDs != nil {
		s.claim = k.LEDs.Claim(led.PriorityMute)
	}
	s.sw.OnCommand = s.set

	s.ring = &esphome.Select{
		Base: esphome.Base{
			ObjectID: "ring_muted",
			Name:     "Ring while muted",
			Icon:     "mdi:microphone-off",
			Category: esphome.CategoryConfig,
		},
	}
	bindEffect(s.ring, led.EffectNames(), s.show, settings.SetRingMuted)

	if muteLED == nil {
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
	return s
}

// entities lists what the mute control exposes.
func (s *muteSwitch) entities() []esphome.Entity {
	ents := []esphome.Entity{s.sw, s.ring}
	if s.brightness == nil {
		return ents
	}
	return append(ents, s.brightness)
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
	if err := settings.SetMicLEDBright(bright); err != nil {
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

// Home Assistant asks for a state, the button on top asks for the other one, and start-up asks for
// whatever was stored. They move the line differently and then want exactly the same things to follow,
// so they share settled: it is not the caller's business what being muted means.

// restore cuts the microphones if they were cut when the device was last on. The line does not survive
// a reboot, so what was stored is applied rather than read — and it is the line that decides, so a
// device whose GPIO cannot be reached comes up live and says so rather than claiming to be muted.
func (s *muteSwitch) restore(saved settings.Stored) {
	muted, err := s.mute.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}

	// What to show while cut, before cutting, so the ring is right the first time settled looks.
	restoreEffect(s.ring, saved.Ring.MutedOr(settings.DefaultMuted), nil,
		settings.SetRingMuted, saved.Ring.Muted != nil)

	want := saved.Microphone.MutedOr(muted)
	if want != muted {
		if err := s.mute.Set(want); err != nil {
			slog.Error("setting mute failed", "muted", want, "err", err)
		}
	}
	s.settled(false)

	slog.Info("restored", "what", "microphone mute", "muted", s.sw.Get(),
		"asked", want, "from", from(saved.Microphone.Muted != nil))

	if s.brightness == nil {
		return
	}
	bright := saved.Microphone.LEDBrightOr(true)
	s.applyBrightness(bright)
	slog.Info("restored", "what", s.brightness.ObjectID, "using", brightnessLabel(bright),
		"from", from(saved.Microphone.LEDBright != nil))
}

// set is the switch in Home Assistant.
func (s *muteSwitch) set(muted bool) {
	if err := s.mute.Set(muted); err != nil {
		slog.Error("setting mute failed", "muted", muted, "err", err)
		return
	}
	s.settled(true)
}

// toggle is the button on top of the device.
func (s *muteSwitch) toggle() {
	if _, err := s.mute.Toggle(); err != nil {
		slog.Error("toggling mute failed", "err", err)
		return
	}
	s.settled(true)
}

// settled publishes what the line now reads — not what was asked for, so a line that did not move says
// so — and shows it on the ring. asked is false at start-up, where nobody asked: there is nothing to
// remember that was not just read from the file, and nothing to announce.
func (s *muteSwitch) settled(asked bool) {
	muted, err := s.mute.Get()
	if err != nil {
		slog.Error("reading mute state failed", "err", err)
		return
	}

	s.sw.Set(muted)
	s.show(chosenEffect(s.ring))

	if !asked {
		return
	}

	if err := settings.SetMicMuted(muted); err != nil {
		slog.Error("saving mute state failed", "err", err)
	}
	slog.Info("microphone mute", "muted", muted)
	if muted {
		chime(s.sound, toneMute)
		return
	}
	chime(s.sound, toneUnmute)
}

// show puts an animation on the ring for as long as the microphones are cut, or takes it off. Unlike a
// failure this has no duration of its own — being muted lasts until it does not — so the claim is held
// and cleared rather than timed.
//
// It takes the name rather than reading the setting, because it is called both when the mute state
// changes and when the choice does, and on that second path the setting has not been written yet.
//
// Off by default, and worth leaving off: the button has its own LED for this, and a ring held lit for
// however long someone leaves the device muted is audible on this hardware as well as visible.
func (s *muteSwitch) show(name string) {
	if s.claim == nil || s.ring == nil {
		return
	}

	if name == "" || !s.sw.Get() {
		s.claim.Clear()
		return
	}
	s.claim.Play(name, mutedColor)
}
