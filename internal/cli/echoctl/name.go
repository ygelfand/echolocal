package echoctl

import (
	"context"
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/device"
	"github.com/ygelfand/echolocal/internal/installer"
)

// resolveName decides what Home Assistant will call the device. A name already on the device
// wins, since changing it later makes Home Assistant treat it as a new device. Otherwise the
// flag is used, or the user is asked. Off a terminal there is nobody to ask, so the flag is
// required rather than guessing a name that is then permanent.
func resolveName(ctx context.Context, out io.Writer, d *device.Device, flag string) (string, error) {
	existing, err := installer.ReadName(d)
	if err != nil {
		return "", err
	}
	if existing != "" {
		return existing, nil
	}
	if flag != "" {
		return flag, nil
	}
	if !isTerminal() {
		return "", fmt.Errorf("%w: pass --name to name this device", installer.ErrNoName)
	}
	return promptName(ctx, out, installer.SuggestName(d))
}

func promptName(ctx context.Context, out io.Writer, suggested string) (string, error) {
	in := textinput.New()
	in.SetValue(suggested)
	in.CharLimit = 31
	in.Focus()

	m := &nameModel{input: in}
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(out)).Run()
	if err != nil {
		return "", err
	}
	n := final.(*nameModel)
	if n.cancelled {
		return "", ErrCancelled
	}
	return n.input.Value(), nil
}

type nameModel struct {
	input     textinput.Model
	cancelled bool
	done      bool
}

func (m *nameModel) Init() tea.Cmd { return textinput.Blink }

func (m *nameModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if m.input.Value() == "" {
				return m, nil
			}
			m.done = true
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *nameModel) View() string {
	if m.done || m.cancelled {
		return ""
	}
	return styleTitle.Render("Name this device") + "\n\n  " + m.input.View() + "\n\n" +
		styleHelp.Render("Home Assistant keys the device on this, so changing it later creates a new one.\n"+
			"lowercase letters, digits, - and _ · enter accept · esc cancel") + "\n"
}

func nameFlag(c *cobra.Command, target *string) {
	c.Flags().StringVar(target, "name", "", "device name Home Assistant sees; asked for if unset")
}
