package installer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ygelfand/echolocal/internal/device"
	"github.com/ygelfand/echolocal/internal/layout"
)

// ErrNoName means neither the device nor the caller supplied a name.
var ErrNoName = errors.New("no device name")

// installName records the name Home Assistant sees. It is written once: Home Assistant keys the
// device on it, so a later change appears as a new device with new entity ids.
func installName(r *run) (string, bool, error) {
	existing, err := ReadName(r.d)
	if err != nil {
		return "", false, err
	}
	if existing != "" {
		return existing, true, nil
	}
	if r.cfg.Name == "" {
		return "", false, fmt.Errorf("%w: pass --name", ErrNoName)
	}
	if err := validName(r.cfg.Name); err != nil {
		return "", false, err
	}

	if _, err := r.d.Shell("mkdir -p " + layout.StateDir); err != nil {
		return "", false, err
	}
	if err := r.d.WriteFile(layout.NamePath, []byte(r.cfg.Name+"\n"), 0o644); err != nil {
		return "", false, err
	}
	return r.cfg.Name, false, nil
}

// ReadName returns the device's configured name, or empty when it has none.
func ReadName(d *device.Device) (string, error) {
	have, err := d.Exists(layout.NamePath)
	if err != nil || !have {
		return "", err
	}
	b, err := d.ReadFile(layout.NamePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// SuggestName derives a unique default from the wlan0 address.
func SuggestName(d *device.Device) string {
	out, err := d.Shell("cat " + layout.MACPath)
	if err != nil {
		return layout.DefaultName
	}
	return layout.NameFromMAC(out)
}

// validName checks the display name can produce a usable node name. The name is stored as typed
// and shown in Home Assistant; the node name it slugifies to becomes the mDNS hostname and the
// entity id prefix.
func validName(name string) error {
	slug := layout.Slug(name)
	if slug == "" {
		return fmt.Errorf("name %q has no letters or digits to build a hostname from", name)
	}
	if len(slug) > layout.MaxNodeName {
		return fmt.Errorf("name %q becomes %q, %d characters, and the limit is %d",
			name, slug, len(slug), layout.MaxNodeName)
	}
	return nil
}
