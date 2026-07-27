package echoctl

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/installer"
)

func newKeyCmd() *cobra.Command {
	var serial string

	c := &cobra.Command{
		Use:   "key",
		Short: "Show or replace the device's encryption key",
		Long: "Home Assistant needs this key to talk to the device. It is generated per device at\n" +
			"install time and stored on the device, so it can be read back rather than saved here.",
	}
	c.PersistentFlags().StringVar(&serial, "serial", "", "device serial, when more than one is connected")

	show := &cobra.Command{
		Use:   "show",
		Short: "Print the key Home Assistant needs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := connect(cmd.Context(), cmd.OutOrStdout(), serial)
			if err != nil {
				return err
			}
			key, err := installer.KeyOrError(d)
			if errors.Is(err, installer.ErrNoKey) {
				fmt.Fprintln(cmd.OutOrStdout(), styleSkip.Render(
					"unprovisioned: no key on the device, Home Assistant can push one"))
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), key)
			return nil
		},
	}

	rotate := &cobra.Command{
		Use:   "rotate",
		Short: "Generate a new key and restart echod",
		Long: "Home Assistant keeps using the old key until you update it there, so the device goes\n" +
			"unreachable until you do.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := connect(cmd.Context(), cmd.OutOrStdout(), serial)
			if err != nil {
				return err
			}
			key, err := installer.RotateKey(d)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, key)
			fmt.Fprintln(out, styleDetail.Render("update this in Home Assistant to reconnect"))
			return nil
		},
	}

	c.AddCommand(show, rotate)
	return c
}
