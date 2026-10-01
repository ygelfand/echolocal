package echod

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/feature/sendspin"
)

func newSendspinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sendspin",
		Short: "Print the room's Sendspin identity and pairing token",
		Long: "The client id is how a Sendspin server such as Music Assistant knows this room. The\n" +
			"pairing token is what its operator enters to pair with it; anyone holding it can pair,\n" +
			"so treat it like a Wi-Fi password. Both are also shown in Home Assistant.\n\n" +
			"Reading them creates the identity if the room does not have one yet.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientID, token, err := sendspin.Credentials()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-15s %s\n", "client id:", clientID)
			fmt.Fprintf(cmd.OutOrStdout(), "%-15s %s\n", "pairing token:", token)
			return nil
		},
	}
}
