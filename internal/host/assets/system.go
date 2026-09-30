package assets

import (
	_ "embed"

	"github.com/ygelfand/echolocal/internal/host/bootimg"
)

// What an install writes to the device, other than echod itself. These are committed rather than
// staged, so every build carries them.

//go:embed boot.img
var bootImageBiscuit []byte

//go:embed boot-radar.img
var bootImageRadar []byte

//go:embed patch/default.prop
var defaultProp []byte

//go:embed patch/fstab.mt8163
var fstab []byte

// BootImage returns the shipped boot image for a device, or an empty slice when none is shipped.
// Callers compare what comes back against the metadata in bootimg.Ours[device]: the size has to
// match for the install to go ahead. The radar placeholder is intentionally a tiny file — the
// bootimg.Image for radar_puffin has an empty SHA256, which checkImage refuses before anything is
// written, so the placeholder can never be flashed by accident.
func BootImage(device string) []byte {
	switch device {
	case "radar_puffin":
		return bootImageRadar
	default:
		return bootImageBiscuit
	}
}

// DefaultBootImage returns the shipped biscuit image, for callers that have not yet probed the
// device (the install flow reads the device once before picking).
func DefaultBootImage() []byte { return bootImageBiscuit }

// SupportedDevices lists the ro.product.device values this build carries a shipped boot image for.
// Excludes placeholders — radar is recognised but not shipped, so it does not appear here until a
// real image lands.
func SupportedDevices() []string {
	var out []string
	for device, img := range bootimg.Ours {
		if img.Shipped() {
			out = append(out, device)
		}
	}
	return out
}

// The root filesystem lives on the system partition on this device, so these go there.
func DefaultProp() []byte { return defaultProp }
func Fstab() []byte       { return fstab }
