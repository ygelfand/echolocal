package echoctl

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/host/installer"
)

// resolveName decides what Home Assistant will call the device.
//
// An explicit --name wins, because someone who typed it meant it — but renaming is not free: Home
// Assistant keys the device on its name, so the old one becomes a stale device with all its entities,
// and that is worth saying out loud rather than doing quietly. Failing that, a name already on the
// device is kept, and only a device with neither is asked about. Off a terminal there is nobody to
// ask, so the flag is required rather than guessing a name that is then permanent.
func resolveName(ctx context.Context, out io.Writer, d *device.Device, flag string) (string, error) {
	existing, err := installer.ReadName(d)
	if err != nil {
		return "", err
	}
	if flag != "" {
		if existing != "" && existing != flag {
			fmt.Fprintf(out, "%s\n", styleDetail.Render(fmt.Sprintf(
				"renaming %s to %s: Home Assistant will see a new device and keep the old one", existing, flag)))
		}
		return flag, nil
	}
	if existing != "" {
		return existing, nil
	}
	if !isTerminal() {
		return "", fmt.Errorf("%w: pass --name to name this device", installer.ErrNoName)
	}
	return promptName(ctx, out, installer.SuggestName(d))
}

func promptName(ctx context.Context, out io.Writer, suggested string) (string, error) {
	fmt.Fprintf(out, "%s\n",
		styleDetail.Render("Home Assistant keys the device on its name, so changing it later creates a new one."))

	return line(ctx, out, "Name this device", suggested, false)
}

func nameFlag(c *cobra.Command, target *string) {
	c.Flags().StringVar(target, "name", "", "device name Home Assistant sees; asked for if unset")
}
