package installer

import (
	"errors"
	"fmt"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/layout"
)

// ErrNoKey means the device is running unprovisioned.
var ErrNoKey = errors.New("device has no encryption key")

// installKey gives the device a per-device key, generated here so it can be shown once for
// pasting into Home Assistant. An existing key is never replaced: rotating it silently would
// break the pairing without saying so.
func installKey(r *run) (string, bool, error) {
	existing, err := ReadKey(r.d)
	if err != nil {
		return "", false, err
	}
	if existing != "" {
		return "already provisioned", true, nil
	}
	if r.cfg.ZeroPSK {
		return "unprovisioned, waiting for Home Assistant to push a key", false, nil
	}

	k, err := esphome.GeneratePSK()
	if err != nil {
		return "", false, err
	}
	if err := writeKey(r.d, k.String()); err != nil {
		return "", false, err
	}
	// The key itself is shown once at the end, where it cannot be missed.
	return "generated", false, nil
}

// ReadKey returns the device's key, or empty when it has none.
func ReadKey(d *device.Device) (string, error) {
	have, err := d.Exists(layout.KeyPath)
	if err != nil || !have {
		return "", err
	}
	b, err := d.ReadFile(layout.KeyPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// RotateKey replaces the device's key and returns the new one. Home Assistant has to be told the
// new value, so this is deliberately explicit rather than part of an install.
func RotateKey(d *device.Device) (string, error) {
	k, err := esphome.GeneratePSK()
	if err != nil {
		return "", err
	}
	if err := writeKey(d, k.String()); err != nil {
		return "", err
	}
	if err := d.Setprop("ctl.restart", layout.ServiceName); err != nil {
		return "", err
	}
	return k.String(), nil
}

func writeKey(d *device.Device, key string) error {
	if _, err := d.Shell("mkdir -p " + layout.StateDir); err != nil {
		return err
	}
	return d.WriteFile(layout.KeyPath, []byte(key+"\n"), 0o600)
}

// KeyOrError is ReadKey with a typed error, for commands that cannot proceed without one.
func KeyOrError(d *device.Device) (string, error) {
	k, err := ReadKey(d)
	if err != nil {
		return "", err
	}
	if k == "" {
		return "", fmt.Errorf("%w (%s missing)", ErrNoKey, layout.KeyPath)
	}
	return k, nil
}
