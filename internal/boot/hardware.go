package boot

import (
	"fmt"
	"os"
	"strings"

	"github.com/ygelfand/echolocal/internal/layout"
)

// listenAddr is the port the installer opened in the firewall.
func listenAddr() string { return fmt.Sprintf(":%d", layout.Port) }

// name prefers what echoctl recorded at install, since Home Assistant keys the device on it.
func name(override string) string {
	if override != "" {
		return override
	}
	if b, err := os.ReadFile(layout.NamePath); err == nil {
		if recorded := strings.TrimSpace(string(b)); recorded != "" {
			return recorded
		}
	}

	b, err := os.ReadFile(layout.MACPath)
	if err != nil {
		return layout.DefaultName
	}
	return layout.NameFromMAC(string(b))
}
