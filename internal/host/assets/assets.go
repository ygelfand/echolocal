// Package assets holds what echoctl ships inside itself: echod, the boot image, and the files an
// install writes onto the system partition.
//
// A release is one download with nothing to fetch and no paths to get wrong. echod is the one thing
// that has to be built, so a build without it staged is still buildable — the accessor comes back
// empty and the caller says what is missing.
package assets

// Echod is the arm binary installed to /system/app/echod, or empty in a build without a payload.
func Echod() []byte { return echod }

// Embedded reports whether this build carries echod.
func Embedded() bool { return len(echod) > 0 }
