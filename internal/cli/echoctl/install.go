package echoctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/host/assets"
	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/host/installer"
)

var (
	styleDone   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleSkip   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleFail   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleActive = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleDetail = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleCount  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))

	styleKeyBox = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")).
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("42")).
			Padding(0, 2).MarginLeft(2)
)

func newInstallCmd() *cobra.Command {
	var (
		serial    string
		echod     string
		name      string
		bootImage string
		ssid      string
		password  string
		wps       bool
		zeroPSK   bool
		flashOnly bool
		assumeYes bool
		doReboot  bool
		noReboot  bool
	)

	c := &cobra.Command{
		Use:   "install",
		Short: "Install echod on a connected Echo Dot",
		Long: "Installs echod into /system/app/echod and hands it Amazon's ledcontroller service,\n" +
			"so init starts and supervises it. Safe to re-run.\n\n" +
			"Begins by checking that the device has root and a permissive kernel, without which\n" +
			"there is no root adbd and no way for echod to open its socket. A device that has both\n" +
			"is left alone; one that does not has EchoLocal's boot image written from recovery,\n" +
			"which needs TWRP as the recovery partition.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			// Not connect: the flash stage is what grants root, so demanding it first would refuse
			// every device that still needs one.
			d, err := attach(cmd.Context(), out, serial)
			if err != nil {
				return err
			}

			cfg := installer.Config{ZeroPSK: zeroPSK}

			// The image is only resolved when it is going to be written. A device that already has root
			// and a permissive kernel needs none, so a build that ships no payload can still install to
			// one.
			state, err := installer.Probe(d)
			if err != nil {
				return err
			}
			if !state.Ready {
				if cfg.BootImage, cfg.BootImageFrom, err = payload(assets.BootImage(), bootImage, "boot image"); err != nil {
					return err
				}
				if cfg.Approved, err = approveFlash(cmd.Context(), out, d, state, cfg.BootImageFrom, assumeYes); err != nil {
					return err
				}
			}

			// The boot image first, and on its own: until it is written there is no root adbd, and
			// everything below needs one — reading the device's own name included.
			// Named for what it always does rather than for what it sometimes does: most runs find a
			// device that already has root and a permissive kernel and write nothing at all.
			if err := render(cmd.Context(), out, "Verifying device boot status",
				"✓ device has root and a permissive kernel",
				func(report installer.Reporter) error {
					return installer.FlashBoot(cmd.Context(), d, cfg, report)
				}); err != nil {
				return err
			}
			if flashOnly {
				return nil
			}

			if cfg.Echod, _, err = payload(assets.Echod(), echod, "echod binary"); err != nil {
				return err
			}
			chosen, err := resolveName(cmd.Context(), out, d, name)
			if err != nil {
				return err
			}
			cfg.Name = chosen

			// settles is whether anything changed that only takes effect on the next boot, which the
			// installer decides: it is the only thing that knows a run replaced the binary from a run
			// that gated a service or hid a package that was running.
			var settles bool
			if err := render(cmd.Context(), out, "Installing EchoLocal",
				"✓ echod installed and running",
				func(report installer.Reporter) error {
					var err error
					settles, err = installer.Install(cmd.Context(), d, cfg, report)
					return err
				}); err != nil {
				return err
			}

			// Wifi before the reboot, because the supplicant keeps what it is given and a device that
			// comes back on the network is more of the install proven. Home Assistant cannot reach the
			// device without it, and a device that never gets a network is still installed, so declining
			// leaves it for `echoctl wifi`.
			if err := ensureWifi(cmd.Context(), out, d, ssid, password, wps); err != nil {
				if !errors.Is(err, ErrCancelled) {
					return err
				}
				fmt.Fprintf(out, "%s\n", styleSkip.Render("• wireless left unconfigured; run `echoctl wifi` when ready"))
			}

			// Before the pairing key rather than after, so the one thing to carry off the screen is the
			// last thing printed.
			if err := offerReboot(cmd.Context(), out, d, settles, rebootChoiceOf(doReboot, noReboot)); err != nil {
				return err
			}
			return printPairing(out, d, chosen)
		},
	}

	c.Flags().StringVar(&serial, "serial", "", "device serial, when more than one is connected")
	c.Flags().StringVar(&echod, "echod", "", "echod binary to install, instead of the one shipped")
	c.Flags().StringVar(&bootImage, "boot-image", "", "boot image to write, instead of the one shipped")
	c.Flags().StringVar(&ssid, "ssid", "", "network to join, instead of picking from a scan")
	c.Flags().StringVar(&password, "password", "", "passphrase, for an --ssid that needs one")
	c.Flags().BoolVar(&wps, "wps", false, "join by pressing the router's WPS button instead")
	c.Flags().BoolVar(&flashOnly, "flash-only", false,
		"write the boot image and stop, without installing echod")
	c.Flags().BoolVarP(&assumeYes, "yes", "y", false,
		"do not ask before overwriting the boot partition")
	c.Flags().BoolVar(&zeroPSK, "zero-psk", false,
		"leave the device unprovisioned so Home Assistant can push a key, instead of generating one")
	c.Flags().BoolVar(&doReboot, "reboot", false, "reboot at the end without asking")
	c.Flags().BoolVar(&noReboot, "no-reboot", false, "finish without rebooting, and without asking")
	c.MarkFlagsMutuallyExclusive("reboot", "no-reboot")
	nameFlag(c, &name)
	return c
}

func newUninstallCmd() *cobra.Command {
	var serial string

	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Restore Amazon's ledcontroller and remove echod",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := connect(cmd.Context(), cmd.OutOrStdout(), serial)
			if err != nil {
				return err
			}
			return render(cmd.Context(), cmd.OutOrStdout(), "Uninstalling EchoLocal",
				"✓ echod removed, ledcontroller restored",
				func(report installer.Reporter) error {
					return installer.Uninstall(cmd.Context(), d, report)
				})
		},
	}

	c.Flags().StringVar(&serial, "serial", "", "device serial, when more than one is connected")
	return c
}

// printPairing shows what Home Assistant needs. The key is the one thing a user has to carry
// off this screen, so it gets a box of its own rather than a line of step detail.
func printPairing(out io.Writer, d *device.Device, name string) error {
	key, err := installer.ReadKey(d)
	if err != nil {
		return err
	}

	if key == "" {
		fmt.Fprintf(out, "\n%s\n", styleDetail.Render(
			"unprovisioned: Home Assistant will push an encryption key on first connection"))
		return nil
	}

	fmt.Fprintf(out, "\n%s\n%s\n",
		styleTitle.Render("Add to Home Assistant"),
		styleDetail.Render("  Settings → Devices → ESPHome → "+name+", then paste this key:"))
	fmt.Fprintln(out, styleKeyBox.Render(key))
	return nil
}

// render drives a step run, live when attached to a terminal and plain lines otherwise.
func render(ctx context.Context, out io.Writer, title, success string, run func(installer.Reporter) error) error {
	if !isTerminal() {
		return run(func(e installer.Event) {
			if e.Status == installer.Running {
				return
			}
			fmt.Fprintf(out, "[%d/%d] %s %s %s\n", e.Step, e.Total, statusWord(e.Status), e.Name, detailOf(e))
		})
	}

	m := &installModel{title: title, success: success, spin: spinner.New(spinner.WithSpinner(spinner.Dot))}
	m.spin.Style = styleActive

	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(out))
	go func() { p.Send(doneMsg{err: run(func(e installer.Event) { p.Send(e) })}) }()

	final, err := p.Run()
	if err != nil {
		return err
	}
	return final.(*installModel).err
}

type doneMsg struct{ err error }

type installModel struct {
	title    string
	success  string
	spin     spinner.Model
	events   []installer.Event
	active   *installer.Event
	err      error
	finished bool
}

func (m *installModel) Init() tea.Cmd { return m.spin.Tick }

func (m *installModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case installer.Event:
		if msg.Status == installer.Running {
			e := msg
			m.active = &e
		} else {
			m.active = nil
			m.events = append(m.events, msg)
		}
		return m, nil

	case doneMsg:
		m.err, m.finished, m.active = msg.err, true, nil
		return m, tea.Quit

	case tea.KeyMsg:
		// Cancelling is a failure, and has to say so: without an error here the view takes the
		// success branch and the command exits 0, claiming to have done what it was interrupted
		// half way through.
		if msg.String() == "ctrl+c" {
			m.err, m.finished = ErrCancelled, true
			return m, tea.Quit
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *installModel) View() tea.View {
	var b strings.Builder
	b.WriteString(styleTitle.Render(m.title) + "\n\n")

	for _, e := range m.events {
		fmt.Fprintf(&b, "  %s %s %s\n", mark(e.Status), e.Name, detailOf(e))
	}
	if m.active != nil {
		fmt.Fprintf(&b, "  %s %s %s\n", m.spin.View(), m.active.Name,
			styleCount.Render(fmt.Sprintf("(%d/%d)", m.active.Step, m.active.Total)))
	}

	if m.finished {
		if m.err != nil {
			b.WriteString("\n" + styleFail.Render("✗ failed: "+m.err.Error()) + "\n")
		} else {
			b.WriteString("\n" + styleDone.Render(m.success) + "\n")
		}
	}
	return tea.NewView(b.String())
}

func mark(st installer.Status) string {
	switch st {
	case installer.Done:
		return styleDone.Render("✓")
	case installer.Skipped:
		return styleSkip.Render("•")
	case installer.Failed:
		return styleFail.Render("✗")
	}
	return " "
}

func statusWord(st installer.Status) string {
	switch st {
	case installer.Done:
		return "ok"
	case installer.Skipped:
		return "skip"
	case installer.Failed:
		return "FAIL"
	}
	return ""
}

func detailOf(e installer.Event) string {
	if e.Err != nil {
		return styleFail.Render(e.Err.Error())
	}
	if e.Detail == "" {
		return ""
	}
	return styleDetail.Render(e.Detail)
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
