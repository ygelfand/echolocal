// Package led drives the Echo Dot's ring through the is31fl3236 driver's sysfs attributes.
//
// This deliberately bypasses Amazon's LedController service. A direct frame write works even
// when that service has brightness set to 0, and keeps working once the Amazon stack is
// disabled — which the binder path does not.
package led

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultPath is the is31fl3236 i2c device directory.
const DefaultPath = "/sys/bus/i2c/devices/0-003f"

// Channels is the number of PWM outputs: 12 ring segments x RGB.
const Channels = 36

// Segments is the number of addressable RGB positions on the ring.
const Segments = 12

// Layout, established by walking channels and then segments:
//
//   - The 36 channels are consecutive RGB triplets in R, G, B order, so segment n occupies
//     channels 3n, 3n+1, 3n+2.
//   - Segment 0 is at the bottom-right of the top face, between the microphone and
//     volume-down buttons.
//   - Increasing segment index runs clockwise, 30 degrees per segment.
//   - Each channel is a linear 0-255 brightness, 0 being dark.

// Color is one segment's color.
type Color struct{ R, G, B byte }

// SetSegments writes all 12 segments at once.
func (r *Ring) SetSegments(c []Color) error {
	if len(c) != Segments {
		return fmt.Errorf("led: need %d segments, got %d", Segments, len(c))
	}
	buf := make([]byte, Channels)
	for i, col := range c {
		buf[i*3], buf[i*3+1], buf[i*3+2] = col.R, col.G, col.B
	}
	return r.SetFrame(buf)
}

// SetSegment lights one segment and blanks the rest.
func (r *Ring) SetSegment(n int, c Color) error {
	if n < 0 || n >= Segments {
		return fmt.Errorf("led: segment %d out of range 0-%d", n, Segments-1)
	}
	all := make([]Color, Segments)
	all[n] = c
	return r.SetSegments(all)
}

// SetAll paints every segment the same color.
func (r *Ring) SetAll(c Color) error {
	all := make([]Color, Segments)
	for i := range all {
		all[i] = c
	}
	return r.SetSegments(all)
}

// Ring writes whole frames to the LED driver.
type Ring struct {
	Path string
}

func New() *Ring { return &Ring{Path: DefaultPath} }

func (r *Ring) path() string {
	if r.Path == "" {
		return DefaultPath
	}
	return r.Path
}

func (r *Ring) attr(name string) string { return r.path() + "/" + name }

// SetFrame writes all 36 channel brightnesses: twelve segments, three channels each.
func (r *Ring) SetFrame(vals []byte) error {
	if len(vals) != Channels {
		return fmt.Errorf("led: need %d channels, got %d", Channels, len(vals))
	}
	return os.WriteFile(r.attr("frame"), []byte(hex.EncodeToString(vals)+"\n"), 0o644)
}

// Frame reads back the current frame.
func (r *Ring) Frame() ([]byte, error) {
	b, err := os.ReadFile(r.attr("frame"))
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimSpace(string(b)))
}

// Fill sets every channel to the same value.
func (r *Ring) Fill(v byte) error {
	buf := make([]byte, Channels)
	for i := range buf {
		buf[i] = v
	}
	return r.SetFrame(buf)
}

// Off blanks the ring.
func (r *Ring) Off() error { return r.Fill(0) }

// Current reads the global drive-current setting.
func (r *Ring) Current() (int, error) {
	b, err := os.ReadFile(r.attr("led_current"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// SetCurrent sets the global drive current.
func (r *Ring) SetCurrent(v int) error {
	return os.WriteFile(r.attr("led_current"), []byte(strconv.Itoa(v)), 0o644)
}

// SetBootAnimation toggles the driver's built-in boot animation.
func (r *Ring) SetBootAnimation(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return os.WriteFile(r.attr("boot_animation"), []byte(v), 0o644)
}
