// Package gpio drives sysfs GPIOs, and the Echo Dot's hardware microphone mute in
// particular.
//
// The mute line is MTK pin 87, which gpiolib never claims — it does not appear in
// /sys/kernel/debug/gpio and no driver owns it, so exporting it by number is what makes it
// reachable. Full pin state is visible in /sys/class/gpio/gpio445/device/mt_gpio.
package gpio

import (
	"fmt"
	"os"
	"strings"
)

const sysfsBase = "/sys/class/gpio"

// Pin numbers are MediaTek pin indices, not Linux GPIO numbers. Linux allocates its
// numbering downward from ARCH_NR_GPIOS, so on this unit the single controller
// (1000b000.pinctrl, 155 lines) lands at base 357 and MTK pin 87 becomes gpio444. That base
// is an allocation artifact and can move; the MTK pin index is the stable identity, so
// resolve through the controller rather than hardcoding.
const (
	// MutePin cuts the microphones when high and lights the mute button.
	//
	// The cut is physical — muted capture measures below the room noise floor — but the
	// switch is software-controlled, so it is not a tamper-proof interlock. Whatever can
	// write it can also clear it.
	MutePin = 87

	// MuteLEDPin only dims the mute button LED and cannot switch it off, because the LED
	// is lit by the mute line itself.
	MuteLEDPin = 88
)

// chipBase reads the base of the SoC pin controller. Falls back to the observed value if
// sysfs does not report one.
func chipBase() int {
	entries, err := os.ReadDir(sysfsBase)
	if err != nil {
		return 357
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "gpiochip") {
			continue
		}
		b, err := os.ReadFile(sysfsBase + "/" + e.Name() + "/base")
		if err != nil {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &n); err == nil {
			return n
		}
	}
	return 357
}

// Line converts an MTK pin index to its Linux GPIO number.
func Line(mtkPin int) int { return chipBase() + mtkPin }

// Pin is one sysfs GPIO.
type Pin struct{ N int }

func (p Pin) dir() string             { return fmt.Sprintf("%s/gpio%d", sysfsBase, p.N) }
func (p Pin) path(attr string) string { return p.dir() + "/" + attr }

func (p Pin) exported() bool {
	_, err := os.Stat(p.dir())
	return err == nil
}

// Export makes the pin visible in sysfs. Already exported is not an error.
func (p Pin) Export() error {
	if p.exported() {
		return nil
	}
	if err := os.WriteFile(sysfsBase+"/export", []byte(fmt.Sprint(p.N)), 0o644); err != nil {
		if !p.exported() {
			return err
		}
	}
	return nil
}

func (p Pin) SetDirection(d string) error {
	return os.WriteFile(p.path("direction"), []byte(d), 0o644)
}

func (p Pin) Direction() (string, error) {
	b, err := os.ReadFile(p.path("direction"))
	return strings.TrimSpace(string(b)), err
}

func (p Pin) Set(v bool) error {
	s := "0"
	if v {
		s = "1"
	}
	return os.WriteFile(p.path("value"), []byte(s), 0o644)
}

func (p Pin) Get() (bool, error) {
	b, err := os.ReadFile(p.path("value"))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(b)) == "1", nil
}

// Mute controls the hardware microphone cut.
type Mute struct{ pin Pin }

// NewMute prepares the mute line for driving without disturbing its current state, so
// constructing this can never silently unmute a muted device.
func NewMute() (*Mute, error) {
	p := Pin{N: Line(MutePin)}
	if err := p.Export(); err != nil {
		return nil, fmt.Errorf("export mute line: %w", err)
	}
	// Exporting resets direction to "in", which floats the line. Sample the level first,
	// then drive it back to what it was.
	cur, err := p.Get()
	if err != nil {
		return nil, err
	}
	if err := p.SetDirection("out"); err != nil {
		return nil, fmt.Errorf("set mute line to output: %w", err)
	}
	if err := p.Set(cur); err != nil {
		return nil, err
	}
	return &Mute{pin: p}, nil
}

// Set cuts the microphones when true.
func (m *Mute) Set(muted bool) error { return m.pin.Set(muted) }

// Get reports whether the microphones are currently cut.
func (m *Mute) Get() (bool, error) { return m.pin.Get() }

// Toggle flips the state and returns the new one, which is what a button press wants.
func (m *Mute) Toggle() (bool, error) {
	cur, err := m.Get()
	if err != nil {
		return false, err
	}
	next := !cur
	return next, m.Set(next)
}
