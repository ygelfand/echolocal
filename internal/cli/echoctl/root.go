// Package echoctl is the command tree for the host-side CLI.
package echoctl

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ygelfand/echolocal/internal/layout"
)

var cfgFile string

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "echoctl",
		Short: "EchoLocal host CLI",
		Long: "echoctl installs and manages EchoLocal on an Echo Dot, and provides host-side\n" +
			"tools for working with captures taken from one.",
		SilenceUsage: true,
		Version:      layout.VersionString(),
	}

	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.echolocal.yaml)")
	cobra.OnInitialize(initConfig)

	root.AddCommand(newInstallCmd())
	root.AddCommand(newUninstallCmd())
	root.AddCommand(newWifiCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newRestartCmd())
	root.AddCommand(newKeyCmd())
	root.AddCommand(newToolsCmd())
	return root
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			viper.AddConfigPath(home)
			viper.AddConfigPath(filepath.Join(home, ".config", "echolocal"))
		}
		viper.AddConfigPath(".")
		viper.SetConfigName(".echolocal")
		viper.SetConfigType("yaml")
	}
	viper.SetEnvPrefix("ECHOLOCAL")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintln(os.Stderr, "config:", err)
		}
	}
}

func Execute() {
	root := newRoot()
	if !isTerminal() {
		root.SetOut(colorprofile.NewWriter(os.Stdout, os.Environ()))
		root.SetErr(colorprofile.NewWriter(os.Stderr, os.Environ()))
	}

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
