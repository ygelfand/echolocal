package installer

import (
	"fmt"
	"strings"

	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/layout"
)

// firewallHook opens the API port. Amazon's firewall.sh sets INPUT policy DROP and allows only
// its own ports, so Home Assistant cannot reach us without this.
//
// It runs on every firewall.sh invocation, after the flush, so the rule survives anything that
// rebuilds the chain.
var firewallHook = fmt.Sprintf(`#!/system/bin/sh
# Installed by EchoLocal. Amazon's firewall.sh runs this hook if it exists.
IPTABLES=/system/bin/iptables
$IPTABLES -A INPUT -i wlan0 -p tcp --dport %d -j ACCEPT
`, layout.Port)

// installFirewallHook writes the hook, and applies the rule now so the port opens without
// waiting for a reboot.
func installFirewallHook(r *run) (string, bool, error) {
	existing, err := r.d.Exists(layout.FirewallHook)
	if err != nil {
		return "", false, err
	}
	if existing {
		current, err := r.d.ReadFile(layout.FirewallHook)
		if err != nil {
			return "", false, err
		}
		if string(current) == firewallHook {
			if err := applyFirewallRule(r.d); err != nil {
				return "", false, err
			}
			return "already installed", true, nil
		}
		// Someone else's hook, or an older one of ours. Ours is the only thing that should be
		// there, but say so rather than silently replacing a file we did not write.
		if !strings.Contains(string(current), "EchoLocal") {
			return "", false, fmt.Errorf("%s exists and is not ours", layout.FirewallHook)
		}
	}

	if err := r.d.WriteFile(layout.FirewallHook, []byte(firewallHook), 0o755); err != nil {
		return "", false, err
	}
	if err := r.d.Chcon(layout.OurLabel, layout.FirewallHook); err != nil {
		return "", false, err
	}
	if err := applyFirewallRule(r.d); err != nil {
		return "", false, err
	}
	return fmt.Sprintf("tcp/%d open via %s", layout.Port, layout.FirewallHook), false, nil
}

// applyFirewallRule adds the rule to the running chain if it is not already there.
func applyFirewallRule(d *device.Device) error {
	rule := fmt.Sprintf("INPUT -i wlan0 -p tcp --dport %d -j ACCEPT", layout.Port)
	if _, code, err := d.ShellCode("iptables -C " + rule); err != nil {
		return err
	} else if code == 0 {
		return nil
	}
	_, err := d.Shell("iptables -A " + rule)
	return err
}

// removeFirewallHook is the uninstall counterpart: delete the file and drop the live rule.
func removeFirewallHook(r *run) (string, bool, error) {
	have, err := r.d.Exists(layout.FirewallHook)
	if err != nil {
		return "", false, err
	}
	if !have {
		return "nothing to remove", true, nil
	}
	if _, err := r.d.Shell("rm -f " + layout.FirewallHook); err != nil {
		return "", false, err
	}

	rule := fmt.Sprintf("INPUT -i wlan0 -p tcp --dport %d -j ACCEPT", layout.Port)
	if _, code, err := r.d.ShellCode("iptables -C " + rule); err == nil && code == 0 {
		if _, err := r.d.Shell("iptables -D " + rule); err != nil {
			return "", false, err
		}
	}
	return layout.FirewallHook, false, nil
}
