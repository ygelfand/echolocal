// Package prop sets Android system properties by speaking init's property_service
// protocol directly, so echod needs no setprop, start or stop binary on the device.
package prop

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Socket is init's property service endpoint.
const Socket = "/dev/socket/property_service"

// Wire format of bionic's prop_msg on Android 5.1: a command word followed by two
// fixed-width NUL-padded fields. Android 8 replaced this with a length-prefixed
// protocol, so this is version-specific and correct only through API 25.
const (
	cmdSetProp = 1
	nameMax    = 32
	valueMax   = 92
	msgLen     = 4 + nameMax + valueMax
)

// Set assigns a system property. init applies its own permission checks, so a rejected
// write reports no error here — verify the effect, not the call.
func Set(name, value string) error {
	if len(name) >= nameMax {
		return fmt.Errorf("prop: name %q exceeds %d bytes", name, nameMax-1)
	}
	if len(value) >= valueMax {
		return fmt.Errorf("prop: value for %q exceeds %d bytes", name, valueMax-1)
	}

	c, err := net.DialTimeout("unix", Socket, 2*time.Second)
	if err != nil {
		return fmt.Errorf("prop: dial: %w", err)
	}
	defer c.Close()

	msg := make([]byte, msgLen)
	binary.LittleEndian.PutUint32(msg[:4], cmdSetProp)
	copy(msg[4:4+nameMax-1], name)
	copy(msg[4+nameMax:msgLen-1], value)

	if err := c.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("prop: deadline: %w", err)
	}
	if _, err := c.Write(msg); err != nil {
		return fmt.Errorf("prop: write: %w", err)
	}

	// init closes the connection once it has handled the message. Waiting for that makes
	// the call synchronous, which matters when the next step polls for the effect.
	if _, err := io.Copy(io.Discard, c); err != nil && !errors.Is(err, io.EOF) {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil
		}
		return fmt.Errorf("prop: wait: %w", err)
	}
	return nil
}

// Stop asks init to stop a service.
func Stop(service string) error { return Set("ctl.stop", service) }

// Start asks init to start a service.
func Start(service string) error { return Set("ctl.start", service) }
