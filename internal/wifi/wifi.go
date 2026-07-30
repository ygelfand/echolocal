// Package wifi configures the device's wifi through wpa_cli, which ships on the device.
//
// The supplicant owns the configuration and writes it itself, so nothing here composes a
// wpa_supplicant.conf. Android's framework reads its networks back from the supplicant at startup, so
// one added this way also shows up in Settings.
package wifi

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/echolocal/internal/device"
)

const (
	iface   = "wlan0"
	sockets = "/data/misc/wifi/sockets"
)

// Security is how a network is protected.
//
// What this device can join is fixed by its supplicant, which reports key_mgmt as
// "NONE IEEE8021X WPA-EAP WPA-PSK". There is no SAE, so a WPA3-only network cannot be joined at all,
// while one in WPA2/WPA3 transition mode advertises PSK alongside SAE and joins as PSK.
type Security int

const (
	Open Security = iota
	PSK
	SAE
	Enterprise
)

func (s Security) String() string {
	switch s {
	case Open:
		return "open"
	case PSK:
		return "WPA2"
	case SAE:
		return "WPA3"
	}
	return "enterprise"
}

// Supported reports whether this device's supplicant can join.
func (s Security) Supported() bool { return s == Open || s == PSK }

// security reads the flags a scan reports. PSK wins over SAE: an access point advertising both is in
// transition mode, and PSK is the half this device can use.
func security(flags string) Security {
	switch {
	case strings.Contains(flags, "EAP"):
		return Enterprise
	case strings.Contains(flags, "PSK"):
		return PSK
	case strings.Contains(flags, "SAE"):
		return SAE
	}
	return Open
}

// Network is one access point a scan found.
type Network struct {
	SSID     string
	Signal   int
	Flags    string
	Security Security
}

func (n Network) String() string {
	if !n.Security.Supported() {
		return fmt.Sprintf("%s (%d dBm, %s — not supported)", n.SSID, n.Signal, n.Security)
	}
	return fmt.Sprintf("%s (%d dBm, %s)", n.SSID, n.Signal, n.Security)
}

// Scan lists what the device can see, strongest first, one entry per name. Several access points share
// a name in any real building, and a name is what a person picks.
func Scan(ctx context.Context, d *device.Device) ([]Network, error) {
	if _, err := run(d, "scan"); err != nil {
		return nil, err
	}

	// Results arrive over a few seconds, so the list is read until it stops growing.
	var best map[string]Network
	for range 4 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}

		out, err := run(d, "scan_results")
		if err != nil {
			return nil, err
		}
		found := parse(out)
		if len(found) <= len(best) {
			break
		}
		best = found
	}

	networks := make([]Network, 0, len(best))
	for _, n := range best {
		networks = append(networks, n)
	}
	slices.SortFunc(networks, func(a, b Network) int { return b.Signal - a.Signal })
	return networks, nil
}

// parse reads scan_results, keeping the strongest sighting of each name and dropping the unnamed ones,
// which are access points hiding their SSID.
func parse(out string) map[string]Network {
	best := map[string]Network{}
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 5 || fields[0] == "bssid / frequency / signal level / flags / ssid" {
			continue
		}

		ssid := fields[4]
		signal, err := strconv.Atoi(fields[2])
		if ssid == "" || err != nil {
			continue
		}
		if seen, ok := best[ssid]; !ok || signal > seen.Signal {
			best[ssid] = Network{SSID: ssid, Signal: signal, Flags: fields[3], Security: security(fields[3])}
		}
	}
	return best
}

// Configured is the names the supplicant already has.
func Configured(d *device.Device) ([]string, error) {
	out, err := run(d, "list_networks")
	if err != nil {
		return nil, err
	}

	var names []string
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 2 || fields[0] == "network id / ssid / bssid / flags" {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err == nil && fields[1] != "" {
			names = append(names, fields[1])
		}
	}
	return names, nil
}

// Remove forgets every network with this name, so re-joining does not stack duplicates and a failed
// attempt does not stay behind to be retried forever.
func Remove(d *device.Device, ssid string) error {
	out, err := run(d, "list_networks")
	if err != nil {
		return err
	}

	var removed bool
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 2 || fields[1] != ssid {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		if _, err := run(d, "remove_network", fields[0]); err != nil {
			return err
		}
		removed = true
	}

	if !removed {
		return nil
	}
	_, err = run(d, "save_config")
	return err
}

// Join adds a network and selects it. An empty passphrase means an open network.
//
// scan_ssid is set for every network, which costs an active probe and is what makes a hidden one
// joinable at all — the only way onto those, since a scan never names them.
func Join(d *device.Device, ssid, passphrase string) error {
	if err := Remove(d, ssid); err != nil {
		return err
	}

	out, err := run(d, "add_network")
	if err != nil {
		return err
	}
	id := strings.TrimSpace(lastLine(out))
	if _, err := strconv.Atoi(id); err != nil {
		return fmt.Errorf("wifi: add_network said %q", strings.TrimSpace(out))
	}

	set := [][]string{{"ssid", quoted(ssid)}, {"scan_ssid", "1"}}
	if passphrase == "" {
		set = append(set, []string{"key_mgmt", "NONE"})
	} else {
		set = append(set, []string{"psk", quoted(passphrase)})
	}

	for _, kv := range set {
		if _, err := run(d, "set_network", id, kv[0], kv[1]); err != nil {
			_, _ = run(d, "remove_network", id)
			return err
		}
	}
	for _, cmd := range []string{"enable_network", "select_network"} {
		if _, err := run(d, cmd, id); err != nil {
			_, _ = run(d, "remove_network", id)
			return err
		}
	}

	_, err = run(d, "save_config")
	return err
}

// WPS joins by push button, for a router that has one: no name to pick and no passphrase to type.
func WPS(d *device.Device) error {
	if _, err := run(d, "wps_pbc"); err != nil {
		return err
	}
	_, err := run(d, "save_config")
	return err
}

// State is what the supplicant says about the connection.
type State struct {
	SSID    string
	State   string
	Address string
}

// Joined reports whether the device is associated and has an address.
func (s State) Joined() bool { return s.State == "COMPLETED" && s.Address != "" }

func (s State) String() string {
	if s.SSID == "" {
		return s.State
	}
	return fmt.Sprintf("%s, %s, %s", s.SSID, strings.ToLower(s.State), s.Address)
}

// Status reads the current connection.
func Status(d *device.Device) (State, error) {
	out, err := run(d, "status")
	if err != nil {
		return State{}, err
	}

	var s State
	for line := range strings.SplitSeq(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "ssid":
			s.SSID = value
		case "wpa_state":
			s.State = value
		case "ip_address":
			s.Address = value
		}
	}
	return s, nil
}

// Wait blocks until the device has associated and been given an address.
func Wait(ctx context.Context, d *device.Device) (State, error) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	var last State
	for {
		s, err := Status(d)
		if err == nil {
			last = s
			if s.Joined() {
				return s, nil
			}
		}

		select {
		case <-ctx.Done():
			return last, fmt.Errorf("wifi: did not connect (%s): %w", last, ctx.Err())
		case <-tick.C:
		}
	}
}

func run(d *device.Device, args ...string) (string, error) {
	cmd := fmt.Sprintf("wpa_cli -p %s -i %s %s", sockets, iface, strings.Join(args, " "))

	out, err := d.Shell(cmd)
	if err != nil {
		return out, err
	}
	if strings.Contains(out, "FAIL") {
		return out, fmt.Errorf("wifi: %s said %q", args[0], strings.TrimSpace(lastLine(out)))
	}
	return out, nil
}

// quoted is how wpa_cli takes a string value: in double quotes, through one shell.
func quoted(s string) string {
	return "'\"" + strings.ReplaceAll(s, `'`, `'\''`) + "\"'"
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-1]
}
