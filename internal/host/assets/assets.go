// Package assets holds what echoctl ships inside itself: echod, and the boot image that lets it run.
//
// A release is one download with nothing to fetch and no paths to get wrong. Builds without the
// payload staged are still buildable — the accessors come back empty and the caller says what is
// missing — so a plain `go build ./...` needs no 24 MB of binaries in the tree.
package assets

// Echod is the arm binary installed to /system/app/echod, or empty in a build without a payload.
func Echod() []byte { return echod }

// BootImage is the boot image written to boot_a_x, or empty in a build without a payload.
func BootImage() []byte { return bootImage }

// Embedded reports whether this build carries both.
func Embedded() bool { return len(echod) > 0 && len(bootImage) > 0 }
