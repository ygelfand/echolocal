package echoctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ygelfand/echolocal/internal/device"
)

var (
	styleCursor   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	styleSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleHelp     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// ErrCancelled means the user dismissed the picker.
var ErrCancelled = errors.New("cancelled")

// connect resolves which device a command acts on and opens it. Every command goes through
// this, so device selection behaves the same everywhere.
func connect(ctx context.Context, out io.Writer, serial string) (*device.Device, error) {
	target, err := resolveSerial(ctx, out, serial)
	if err != nil {
		return nil, err
	}
	return device.Connect(target)
}

// resolveSerial decides which device to act on. An explicit serial always wins. On a terminal
// the user picks, even when only one device is connected, so it is clear what is about to be
// written to. Off a terminal there is nobody to ask, so selection is left to device.Connect.
func resolveSerial(ctx context.Context, out io.Writer, serial string) (string, error) {
	if serial != "" {
		return serial, nil
	}
	if !isTerminal() {
		return "", nil
	}

	devices, err := device.List()
	if err != nil {
		return "", err
	}
	if len(devices) == 0 {
		return "", errors.New("no device connected; check the USB cable and `adb devices`")
	}
	return pickDevice(ctx, out, devices)
}

func pickDevice(ctx context.Context, out io.Writer, devices []device.Info) (string, error) {
	m := &pickerModel{devices: devices}
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(out)).Run()
	if err != nil {
		return "", err
	}
	p := final.(*pickerModel)
	if p.cancelled {
		return "", ErrCancelled
	}
	return p.devices[p.cursor].Serial, nil
}

type pickerModel struct {
	devices   []device.Info
	cursor    int
	chosen    bool
	cancelled bool
}

func (m *pickerModel) Init() tea.Cmd { return nil }

func (m *pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.devices)-1 {
			m.cursor++
		}
	case "enter":
		m.chosen = true
		return m, tea.Quit
	case "q", "esc", "ctrl+c":
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *pickerModel) View() string {
	if m.chosen || m.cancelled {
		return ""
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("Select a device") + "\n\n")
	for i, d := range m.devices {
		cursor, line := "  ", fmt.Sprintf("%s  %s", d, styleDetail.Render(d.Serial))
		if i == m.cursor {
			cursor = styleCursor.Render("> ")
			line = styleSelected.Render(d.String()) + "  " + styleDetail.Render(d.Serial)
		}
		b.WriteString(cursor + line + "\n")
	}
	b.WriteString("\n" + styleHelp.Render("↑/↓ move · enter select · esc cancel") + "\n")
	return b.String()
}
