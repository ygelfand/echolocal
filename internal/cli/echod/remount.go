package echod

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/update"
)

// newRemountCmd exists because nothing else on the device can do this. The kernel mounts the root
// filesystem itself and names its source /dev/root, for which there is no node, so every mount(8) here
// fails on it; mount(2) with an empty source does not need one. adbd makes that call for `adb remount`
// and this makes it for the other direction, which adb has no command for.
func newRemountCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "remount rw|ro",
		Short:     "Make the system partition writable, or put it back",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"rw", "ro"},
		RunE: func(_ *cobra.Command, args []string) error {
			rw := args[0] == "rw"
			if err := update.Remount(rw); err != nil {
				return err
			}
			fmt.Println(args[0])
			return nil
		},
	}
}
