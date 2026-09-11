package assets

import _ "embed"

// What an install writes to the device, other than echod itself. These are committed rather than
// staged, so every build carries them.

//go:embed boot.img
var bootImage []byte

//go:embed patch/default.prop
var defaultProp []byte

//go:embed patch/fstab.mt8163
var fstab []byte

// BootImage is written to the device's boot slot.
func BootImage() []byte { return bootImage }

// The root filesystem lives on the system partition on this device, so these go there.
func DefaultProp() []byte { return defaultProp }
func Fstab() []byte       { return fstab }
