package echoctl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/echolocal/internal/device"
	"github.com/ygelfand/echolocal/internal/wifi"
)

// joinTimeout covers association and DHCP.
const joinTimeout = 45 * time.Second

func newWifiCmd() *cobra.Command {
	var (
		serial   string
		ssid     string
		password string
		wps      bool
	)

	c := &cobra.Command{
		Use:   "wifi",
		Short: "Connect the device to a wireless network",
		Long: "Scans, joins and reports. The device needs this before Home Assistant can reach it,\n" +
			"and it needs root first, which `install --flash-only` provides.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, out := cmd.Context(), cmd.OutOrStdout()

			d, err := connect(ctx, out, serial)
			if err != nil {
				return err
			}
			return ensureWifi(ctx, out, d, ssid, password, wps)
		},
	}

	c.Flags().StringVar(&serial, "serial", "", "device serial, when more than one is connected")
	c.Flags().StringVar(&ssid, "ssid", "", "network to join, instead of picking from a scan")
	c.Flags().StringVar(&password, "password", "", "passphrase, for an --ssid that needs one")
	c.Flags().BoolVar(&wps, "wps", false, "join by pressing the router's WPS button instead")
	return c
}

// ensureWifi verifies the connection and configures one when there is none. Both the wifi command and
// the end of an install come through here, so a device gets the same treatment either way.
func ensureWifi(ctx context.Context, out io.Writer, d *device.Device, ssid, password string, wps bool) error {
	// What the device is now decides what to offer. One already on a network is left alone unless the
	// flags say otherwise.
	if ssid == "" && !wps {
		done, err := check(ctx, out, d)
		if err != nil || done {
			return err
		}
	}

	var err error
	for {
		if wps {
			fmt.Fprintf(out, "%s\n", styleDetail.Render("Press the WPS button on the router now."))
			if err := wifi.WPS(d); err != nil {
				return err
			}
		} else {
			if ssid == "" {
				if ssid, password, err = askNetwork(ctx, out, d); err != nil {
					return err
				}
			}
			if err := wifi.Join(d, ssid, password); err != nil {
				return err
			}
		}

		state, err := attempt(ctx, d, ssid)
		if err == nil {
			fmt.Fprintf(out, "%s %s\n", styleDone.Render("✓ connected:"), state)
			return nil
		}

		fmt.Fprintf(out, "%s %v\n", styleFail.Render("✗"), err)
		if ssid != "" {
			// Nothing keeps a network that never connected: it would be retried on every boot.
			if err := wifi.Remove(d, ssid); err != nil {
				return err
			}
		}

		again, err := confirm(ctx, out, "Try again?")
		if err != nil || !again {
			return err
		}
		ssid, password, wps = "", "", false
	}
}

// askNetwork scans, has one picked, and asks for a passphrase unless the network is open.
func askNetwork(ctx context.Context, out io.Writer, d *device.Device) (string, string, error) {
	fmt.Fprintf(out, "%s\n", styleDetail.Render("Scanning…"))

	networks, err := wifi.Scan(ctx, d)
	if err != nil {
		return "", "", err
	}
	if len(networks) == 0 {
		return "", "", errors.New("no networks found; pass --ssid to join a hidden one")
	}

	chosen, err := choose(ctx, out, "Which network?", networks, wifi.Network.String, "Other network…")
	if err != nil {
		return "", "", err
	}

	// The zero Network is the "other" row: a hidden access point is never in a scan, so its name has to
	// be typed, and its security is whatever the passphrase implies.
	if chosen.SSID == "" {
		name, err := line(ctx, out, "Network name", "", false)
		if err != nil {
			return "", "", err
		}
		password, err := line(ctx, out, "Passphrase for "+name, "", true)
		return name, password, err
	}

	if !chosen.Security.Supported() {
		return "", "", fmt.Errorf("%s is %s, which this device's supplicant cannot join", chosen.SSID, chosen.Security)
	}
	if chosen.Security == wifi.Open {
		return chosen.SSID, "", nil
	}

	password, err := line(ctx, out, "Passphrase for "+chosen.SSID, "", true)
	return chosen.SSID, password, err
}

// check reports what the device already has, and whether that is the end of it: connected means
// nothing to do, while a network configured but not up is worth asking about rather than replacing.
func check(ctx context.Context, out io.Writer, d *device.Device) (bool, error) {
	state, err := wifi.Status(d)
	if err != nil {
		return false, err
	}
	if state.Joined("") {
		fmt.Fprintf(out, "%s %s\n", styleDone.Render("✓ already connected:"), state)
		return true, nil
	}

	configured, err := wifi.Configured(d)
	if err != nil {
		return false, err
	}
	if len(configured) == 0 {
		return false, nil
	}

	fmt.Fprintf(out, "%s %s\n", styleDetail.Render("configured but not connected:"),
		strings.Join(configured, ", "))
	fmt.Fprintf(out, "%s %s\n", styleDetail.Render("state:"), state)

	again, err := confirm(ctx, out, "Reconfigure?")
	return !again, err
}

// attempt waits out one try at joining ssid and being given an address. An empty ssid is a WPS join,
// where the network was never named and any is what success looks like.
func attempt(ctx context.Context, d *device.Device, ssid string) (wifi.State, error) {
	ctx, cancel := context.WithTimeout(ctx, joinTimeout)
	defer cancel()

	return wifi.Wait(ctx, d, ssid)
}
