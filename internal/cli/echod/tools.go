package echod

import "github.com/spf13/cobra"

func newToolsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "tools",
		Short: "Hardware diagnostics run on the device",
		Long: "Direct access to the Echo Dot's audio and LED hardware, for characterising it\n" +
			"and for checking a unit after install.",
	}
	c.AddCommand(newMicCmd(), newLEDCmd(), newInfoCmd(), newButtonsCmd(), newI2CCmd(), newMuteCmd(),
		newMixerCmd(), newPlayCmd())
	return c
}
