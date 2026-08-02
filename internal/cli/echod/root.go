// Package echod is the command tree for the on-device agent.
package echod

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ygelfand/echolocal/internal/layout"
)

var cfgFile string

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "echod",
		Short: "EchoLocal device agent",
		Long: "echod runs on the Echo Dot and presents it to Home Assistant as an ESPHome\n" +
			"voice satellite. The tools subtree exposes the same hardware access for\n" +
			"diagnostics.",
		SilenceUsage: true,
		Version:      layout.VersionString(),
	}

	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default /system/etc/echolocal/echod.yaml)")
	cobra.OnInitialize(initConfig)

	root.AddCommand(newRunCmd())
	root.AddCommand(newToolsCmd())
	return root
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("echod")
		viper.SetConfigType("yaml")
		viper.AddConfigPath("/system/etc/echolocal")
		viper.AddConfigPath(".")
	}
	viper.SetEnvPrefix("ECHOD")
	viper.AutomaticEnv()

	// A missing config is normal; anything else is worth knowing about.
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintln(os.Stderr, "config:", err)
		}
	}
}

func Execute() {
	// init starts echod from a service definition that passes no arguments, so a bare
	// invocation has to mean "be the agent" rather than print usage and exit.
	if len(os.Args) == 1 {
		os.Args = append(os.Args, "run")
	}
	if err := newRoot().Execute(); err != nil {
		os.Exit(1)
	}
}
