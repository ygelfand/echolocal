package echoctl

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ygelfand/echolocal/internal/host/device"
)

// ErrCancelled means the user dismissed a prompt.
var ErrCancelled = errors.New("cancelled")

// connect resolves which device a command acts on and opens it. Every command goes through
// this, so device selection behaves the same everywhere.
func connect(ctx context.Context, out io.Writer, serial string) (*device.Device, error) {
	target, err := resolveSerial(ctx, out, serial, device.List)
	if err != nil {
		return nil, err
	}
	return device.Connect(target)
}

// attach is connect for the install, which begins by writing the boot image that grants root. It
// cannot demand root up front for the same reason.
//
// A device adb cannot drive is offered a retry rather than refused. An install stopped part way leaves
// one there, and the way back is the device's own buttons; the retry then picks up wherever that lands,
// recovery included.
func attach(ctx context.Context, out io.Writer, serial string) (*device.Device, error) {
	for {
		d, err := attachOnce(ctx, out, serial)
		if !errors.Is(err, device.ErrUnreachable) || !isTerminal() {
			return d, err
		}
		if err := offerRecovery(ctx, out); err != nil {
			return nil, err
		}
	}
}

func attachOnce(ctx context.Context, out io.Writer, serial string) (*device.Device, error) {
	target, err := resolveSerial(ctx, out, serial, device.ListAny)
	if err != nil {
		return nil, err
	}
	return device.AttachAny(target)
}

// offerRecovery says how to get a device back and waits for it to be done.
func offerRecovery(ctx context.Context, out io.Writer) error {
	fmt.Fprintf(out, "\n%s\n", styleTitle.Render("Cannot reach the device"))
	fmt.Fprintf(out, "%s\n", styleDetail.Render(
		"  If the device is up and connected but the adb patches are not installed yet,\n"+
			"  reboot it holding volume up, until the ring turns white."))

	ok, err := confirm(ctx, out, "Try again")
	if err != nil {
		return err
	}
	if !ok {
		return ErrCancelled
	}
	return nil
}

// resolveSerial decides which device to act on. An explicit serial always wins. On a terminal
// the user picks, even when only one device is connected, so it is clear what is about to be
// written to. Off a terminal there is nobody to ask, so selection is left to device.Connect.
func resolveSerial(ctx context.Context, out io.Writer, serial string, list func() ([]device.Info, error)) (string, error) {
	if serial != "" {
		return serial, nil
	}
	if !isTerminal() {
		return "", nil
	}

	devices, err := list()
	if err != nil {
		return "", err
	}
	if len(devices) == 0 {
		return "", device.ErrUnreachable
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
