package echoctl

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ygelfand/echolocal/internal/device"
	"github.com/ygelfand/echolocal/internal/installer"
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

// offerReboot restarts the device when that is wanted, and then waits for echod to come back by itself.
//
// Asked only when the install changed something. Steps that gate init services or hook the firewall
// take effect on the next boot, so a device that was only just installed is not yet the device it will
// be; a re-run that changed nothing has nothing new to prove and should finish silently.
func offerReboot(ctx context.Context, out io.Writer, d *device.Device, changed bool, choice rebootChoice) error {
	switch choice {
	case rebootNo:
		return nil

	case rebootAsk:
		if !changed {
			return nil
		}
		if !isTerminal() {
			fmt.Fprintf(out, "%s\n", styleSkip.Render(
				"• some of this takes effect on the next boot; pass --reboot to have it done here"))
			return nil
		}

		yes, err := confirm(ctx, out,
			"Reboot now? Some of what was installed only takes effect on the next boot.")
		if err != nil && !errors.Is(err, ErrCancelled) {
			return err
		}
		if !yes {
			fmt.Fprintf(out, "%s\n", styleSkip.Render("• not rebooting; the rest lands whenever it next boots"))
			return nil
		}
	}

	_, err := render(ctx, out, "Rebooting", "✓ device came back and echod started on its own",
		func(report installer.Reporter) error {
			return installer.RebootAndWait(ctx, d, report)
		})
	return err
}
