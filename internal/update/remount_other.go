//go:build !linux

package update

import "errors"

// Nothing off the device has a /system to remount, and a test that wants to exercise the rest of this
// points the paths somewhere it can already write.
func remount(bool) error { return errors.New("update: remounting only works on the device") }

func room(int64) error { return nil }
