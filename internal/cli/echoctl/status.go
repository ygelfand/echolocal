package echoctl

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/installer"
	"github.com/ygelfand/echolocal/internal/layout"
)

var styleKey = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Width(12)

func newStatusCmd() *cobra.Command {
	var serial string

	c := &cobra.Command{
		Use:   "status",
		Short: "Show what is installed on a device and whether it is running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := connect(cmd.Context(), cmd.OutOrStdout(), serial)
			if err != nil {
				return err
			}
			s, err := installer.ReadState(d)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, styleTitle.Render("EchoLocal status"))
			row := func(k, v string) { fmt.Fprintf(out, "  %s %s\n", styleKey.Render(k), v) }

			row("device", fmt.Sprintf("%s (%s) %s", s.Model, s.Product, styleDetail.Render(s.Serial)))
			row("android", "SDK "+s.SDK)
			row("uptime", fmt.Sprintf("%.0fs", s.Uptime))

			if s.Name != "" {
				row("name", s.Name+" "+styleDetail.Render("as Home Assistant sees it"))
			} else {
				row("name", styleSkip.Render("unset"))
			}
			if s.Provisioned {
				row("key", styleDone.Render("provisioned")+" "+styleDetail.Render("echoctl key show"))
			} else {
				row("key", styleSkip.Render("unprovisioned"))
			}

			if s.Installed {
				row("installed", styleDone.Render("yes")+" "+styleDetail.Render(s.LinkTarget))
				row("version", s.Version)
			} else if s.LinkTarget != "" {
				row("installed", styleFail.Render("no")+" "+
					styleDetail.Render(layout.Service+" -> "+s.LinkTarget))
			} else {
				row("installed", styleFail.Render("no")+" "+
					styleDetail.Render(layout.Service+" is Amazon's binary"))
			}

			if s.HaveBackup {
				row("backup", styleDone.Render("present")+" "+styleDetail.Render(layout.Backup))
			} else {
				row("backup", styleSkip.Render("none"))
			}

			state := s.ServiceState
			if state == "" {
				state = "unknown"
			}
			if state == "running" {
				row("service", styleDone.Render(state))
			} else {
				row("service", styleFail.Render(state))
			}
			if s.AgentState != "" {
				row("agent", s.AgentState)
			}
			if running, ok := s.RunningFor(); ok {
				row("running", fmt.Sprintf("%s %s", running.Round(time.Second),
					styleDetail.Render("since uptime "+s.StartedAt+"s")))
			}
			fmt.Fprintf(out, "\n%s\n", styleDetail.Render("logs: adb logcat -s "+layout.LogTag))
			return nil
		},
	}

	c.Flags().StringVar(&serial, "serial", "", "device serial, when more than one is connected")
	return c
}

func newRestartCmd() *cobra.Command {
	var serial string

	c := &cobra.Command{
		Use:   "restart",
		Short: "Restart echod through init",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := connect(cmd.Context(), cmd.OutOrStdout(), serial)
			if err != nil {
				return err
			}
			return render(cmd.Context(), cmd.OutOrStdout(), "Restarting echod", "✓ echod restarted",
				func(report installer.Reporter) error {
					return installer.Restart(cmd.Context(), d, report)
				})
		},
	}

	c.Flags().StringVar(&serial, "serial", "", "device serial, when more than one is connected")
	return c
}
