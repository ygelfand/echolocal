package echoctl

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/host/installer"
)

// rebootChoice is what the flags said. Nothing given means ask, which is why this is not a bool.
type rebootChoice int

const (
	rebootAsk rebootChoice = iota
	rebootYes
	rebootNo
)

func rebootChoiceOf(yes, no bool) rebootChoice {
	switch {
	case no:
		return rebootNo
	case yes:
		return rebootYes
	}
	return rebootAsk
}

// offerReboot restarts the device when requested, waits for echod, and reports whether the reboot
// actually happened. Pryon finalization needs that distinction because Android scans a new system APK
// only during boot.
func offerReboot(ctx context.Context, out io.Writer, d *device.Device, settles bool, choice rebootChoice) (bool, error) {
	switch choice {
	case rebootNo:
		return false, nil

	case rebootAsk:
		if !settles {
			return false, nil
		}
		if !isTerminal() {
			fmt.Fprintf(out, "%s\n", styleSkip.Render(
				"• some of this takes effect on the next boot; pass --reboot to have it done here"))
			return false, nil
		}

		yes, err := confirm(ctx, out,
			"Reboot now? Some of what was installed only takes effect on the next boot.")
		if err != nil && !errors.Is(err, ErrCancelled) {
			return false, err
		}
		if !yes {
			fmt.Fprintf(out, "%s\n", styleSkip.Render("• not rebooting; the rest lands whenever it next boots"))
			return false, nil
		}
	}

	err := render(ctx, out, "Rebooting", "✓ device came back and echod started on its own",
		func(report installer.Reporter) error {
			return installer.RebootAndWait(ctx, d, report)
		})
	return err == nil, err
}
