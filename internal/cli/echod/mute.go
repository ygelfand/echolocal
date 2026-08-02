package echod

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/hardware/gpio"
)

func newMuteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "mute [on|off|toggle]",
		Short: "Read or set the hardware microphone mute",
		Long: "Drives MTK pin 87 (gpio444), the line Amazon's firmware uses to cut the mics and\n" +
			"light the mute button. With no argument, reports the current state.\n\n" +
			"The cut is physical: muted capture measures below the room noise floor, not merely\n" +
			"below speech. It needs none of Amazon's services, so it keeps working after\n" +
			"de-Amazoning — and a muted unit stays recoverable, which it would not be if we\n" +
			"depended on their handler.\n\n" +
			"It is still a software-controlled switch rather than a physical interlock: whatever\n" +
			"can mute can also unmute.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := gpio.NewMute()
			if err != nil {
				return err
			}

			if len(args) == 1 {
				switch args[0] {
				case "on", "true", "1":
					err = m.Set(true)
				case "off", "false", "0":
					err = m.Set(false)
				case "toggle":
					_, err = m.Toggle()
				default:
					return fmt.Errorf("expected on, off or toggle; got %q", args[0])
				}
				if err != nil {
					return err
				}
			}

			muted, err := m.Get()
			if err != nil {
				return err
			}
			state := "unmuted (microphones live)"
			if muted {
				state = "MUTED (microphones cut)"
			}
			fmt.Fprintln(cmd.OutOrStdout(), state)
			return nil
		},
	}
	return c
}
