//go:build !linux

package clock

import (
	"errors"
	"time"
)

// The clock is only ever set on the device. On a development host these report that, so the
// component compiles everywhere and the tests can run its selection logic.

var errNotHere = errors.New("clock: only set on the device")

func step(time.Time) error     { return errNotHere }
func slew(time.Duration) error { return errNotHere }
func writeRTC(time.Time) error { return errNotHere }
