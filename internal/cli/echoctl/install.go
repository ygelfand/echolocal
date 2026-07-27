package echoctl

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/device"
	"github.com/ygelfand/echolocal/internal/installer"
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
		serial  string
		echod   string
		name    string
		zeroPSK bool
	)

	c := &cobra.Command{
		Use:   "install",
		Short: "Install echod on a connected Echo Dot",
		Long: "Installs echod into /system/app/echod and hands it Amazon's ledcontroller service,\n" +
			"so init starts and supervises it. Safe to re-run.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := connect(cmd.Context(), cmd.OutOrStdout(), serial)
			if err != nil {
				return err
			}
			chosen, err := resolveName(cmd.Context(), cmd.OutOrStdout(), d, name)
			if err != nil {
				return err
			}
			cfg := installer.Config{EchodPath: echod, Name: chosen, ZeroPSK: zeroPSK}
			if err := render(cmd.Context(), cmd.OutOrStdout(), "Installing EchoLocal",
				"✓ echod installed and running",
				func(report installer.Reporter) error {
					return installer.Install(cmd.Context(), d, cfg, report)
				}); err != nil {
				return err
			}
			return printPairing(cmd.OutOrStdout(), d, chosen)
		},
	}

	c.Flags().StringVar(&serial, "serial", "", "device serial, when more than one is connected")
	c.Flags().StringVar(&echod, "echod", "bin/echod", "path to the echod binary to install")
	c.Flags().BoolVar(&zeroPSK, "zero-psk", false,
		"leave the device unprovisioned so Home Assistant can push a key, instead of generating one")
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
		if msg.String() == "ctrl+c" {
			m.finished = true
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

func (m *installModel) View() string {
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
	return b.String()
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
