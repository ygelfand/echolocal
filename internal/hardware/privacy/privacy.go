// Package privacy is the microphone mute, whichever way this device exposes it.
//
// Fire OS 5 leaves the mute line and the LED line unclaimed, so echod drives them itself through
// sysfs GPIO. Fire OS 6 binds both to the keypad driver — the mute button is the PMIC power key, and
// the driver's own handler performs the cut — so exporting them fails and the driver's interface is
// the only way in. Which one a device has is a question about the device, not about its firmware, so
// it is answered by looking.
package privacy

import (
	"os"
	"time"
)

// Mute is the hardware microphone cut.
type Mute interface {
	Get() (bool, error)
	Set(muted bool) error
	Toggle() (bool, error)

	// HardwareToggles reports whether a button press has already changed the state by the time
	// anything hears about it, which makes acting on the press a race against the driver.
	HardwareToggles() bool

	// Lag is how long the state may take to catch up with a change, so that whatever publishes it
	// waits rather than reporting the state being left.
	Lag() time.Duration
}

// LED is the light in the mute button, which has two levels and no range.
type LED interface {
	SetBright(bright bool) error
	Bright() (bool, error)
}

// Microphone is the mute.
func Microphone() (Mute, error) {
	if present(state) {
		return driver{}, nil
	}
	return exported()
}

// Light is the mute button's LED.
func Light() (LED, error) {
	if present(brightness) {
		return driverLED{}, nil
	}
	return exportedLED()
}

func present(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
