package echoctl

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ygelfand/echolocal/internal/device"
)

// ErrCancelled means the user dismissed a prompt.
var ErrCancelled = errors.New("cancelled")

// connect resolves which device a command acts on and opens it. Every command goes through
// this, so device selection behaves the same everywhere.
func connect(ctx context.Context, out io.Writer, serial string) (*device.Device, error) {
	target, err := resolveSerial(ctx, out, serial)
	if err != nil {
		return nil, err
	}
	return device.Connect(target)
}

// attach is connect for the install, which begins by writing the boot image that grants root. It
// cannot demand root up front for the same reason.
func attach(ctx context.Context, out io.Writer, serial string) (*device.Device, error) {
	target, err := resolveSerial(ctx, out, serial)
	if err != nil {
		return nil, err
	}
	return device.Attach(target)
}

// resolveSerial decides which device to act on. An explicit serial always wins. On a terminal
// the user picks, even when only one device is connected, so it is clear what is about to be
// written to. Off a terminal there is nobody to ask, so selection is left to device.Connect.
func resolveSerial(ctx context.Context, out io.Writer, serial string) (string, error) {
	if serial != "" {
		return serial, nil
	}
	if !isTerminal() {
		return "", nil
	}

	devices, err := device.List()
	if err != nil {
		return "", err
	}
	if len(devices) == 0 {
		return "", errors.New("no device connected; check the USB cable and `adb devices`")
	}
	return pickDevice(ctx, out, devices)
}

func pickDevice(ctx context.Context, out io.Writer, devices []device.Info) (string, error) {
	chosen, err := choose(ctx, out, "Select a device", devices,
		func(d device.Info) string { return fmt.Sprintf("%s  %s", d, d.Serial) }, "")
	if err != nil {
		return "", err
	}
	return chosen.Serial, nil
}
