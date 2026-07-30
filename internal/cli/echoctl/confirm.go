package echoctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ygelfand/echolocal/internal/bootimg"
	"github.com/ygelfand/echolocal/internal/device"
	"github.com/ygelfand/echolocal/internal/installer"
)

// payload resolves what to install: what the build ships, or a file when one was named. A build
// without a payload and no flag is an error rather than a silent nothing.
func payload(shipped []byte, path, what string) ([]byte, string, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		return data, path, err
	}
	if len(shipped) == 0 {
		return nil, "", fmt.Errorf("this build ships no %s: build with `make dist`, or pass its path", what)
	}
	return shipped, "shipped with echoctl", nil
}

// approveFlash asks before the progress display starts, since that owns the terminal.
//
// The answer is typed out rather than a keypress: this is the one thing echoctl does that cannot be
// undone from here.
func approveFlash(ctx context.Context, out io.Writer, d *device.Device, state installer.BootState, image string, assumeYes bool) (bool, error) {
	if assumeYes {
		return true, nil
	}
	if !isTerminal() {
		return false, errors.New("writing the boot partition needs confirmation: run this on a terminal, or pass --yes")
	}

	fmt.Fprintf(out, "\n%s\n", styleTitle.Render("This will overwrite the boot partition"))
	fmt.Fprintf(out, "  device     %s (%s)\n", d.Serial(), state.Summary)
	fmt.Fprintf(out, "  partition  %s\n", bootimg.Partition)
	fmt.Fprintf(out, "  image      %s\n", image)
	fmt.Fprintf(out, "%s\n", styleDetail.Render("  Going back means reflashing a stock boot image by hand."))

	return typed(ctx, out, "Type yes to continue", "yes")
}
