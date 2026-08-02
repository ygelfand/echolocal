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

// offerReboot restarts the device when that is wanted, and then waits for echod to come back by itself.
//
// settles is the installer's own answer to whether anything changed that the device will only act on
// when it next starts: an init service gated, a package hidden while it was running, a boot script
// stubbed, the ledcontroller link newly pointed at echod. It is deliberately not "did any step do
// work" — writing the binary, remounting /system and restarting the service happen on every run and a
// reboot settles none of them, so counting those would raise the question every time and teach anyone
// re-running an install to dismiss it.
func offerReboot(ctx context.Context, out io.Writer, d *device.Device, settles bool, choice rebootChoice) error {
	switch choice {
	case rebootNo:
		return nil

	case rebootAsk:
		if !settles {
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

	return render(ctx, out, "Rebooting", "✓ device came back and echod started on its own",
		func(report installer.Reporter) error {
			return installer.RebootAndWait(ctx, d, report)
		})
}
