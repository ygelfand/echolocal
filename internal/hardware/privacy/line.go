package privacy

import (
	"time"

	"github.com/ygelfand/echolocal/internal/hardware/gpio"
)

// line is the mute as a GPIO echod drives itself. Nothing else moves it, so a button press is echod's
// to act on and the value reads back as soon as it is written.
type line struct{ *gpio.Mute }

func (line) HardwareToggles() bool { return false }

func (line) Lag() time.Duration { return 0 }

func exported() (Mute, error) {
	m, err := gpio.Microphone()
	if err != nil {
		return nil, err
	}
	return line{m}, nil
}

func exportedLED() (LED, error) {
	l, err := gpio.LED()
	if err != nil {
		return nil, err
	}
	return l, nil
}
