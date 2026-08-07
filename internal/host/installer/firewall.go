package installer

import (
	"fmt"
	"strings"

	"github.com/ygelfand/echolocal/internal/android/firewall"
	"github.com/ygelfand/echolocal/internal/host/device"
	"github.com/ygelfand/echolocal/internal/layout"
)

// firewallHook opens the API port. Amazon's firewall.sh sets INPUT policy DROP and allows only
// its own ports, so Home Assistant cannot reach us without this.
//
// It runs on every firewall.sh invocation, after the flush, so this is what puts our rules back
// whenever something rebuilds the chain.
//
// Everything of ours goes in a chain of our own, which is what makes uninstalling honest: one jump to
// remove and one chain to delete, rather than hunting for rules by port. The chain is emptied first so
// the result is the same however many times this runs — and so a port opened by hand, like remote adb,
// closes again when the firewall is rebuilt, which was already true when the rule went into INPUT.
//
// The jump is inserted, not appended: INPUT ends in the vendor's DROP, and a rule after it never runs.
var firewallHook = fmt.Sprintf(`#!/system/bin/sh
# Installed by EchoLocal. Amazon's firewall.sh runs this hook if it exists.
IPTABLES=/system/bin/iptables
CHAIN=%s
$IPTABLES -N $CHAIN 2>/dev/null
$IPTABLES -F $CHAIN
$IPTABLES -A $CHAIN -i wlan0 -p tcp --dport %d -j ACCEPT
$IPTABLES -C INPUT -i wlan0 -j $CHAIN 2>/dev/null || $IPTABLES -I INPUT -i wlan0 -j $CHAIN
`, firewall.Chain, layout.Port)

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

// applyFirewallRule runs the hook's own work now, so the port opens without waiting for a reboot.
// Running the installed script is the only way to be sure the two cannot disagree.
func applyFirewallRule(d *device.Device) error {
	_, err := d.Shell("sh " + layout.FirewallHook)
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

	// The whole point of the chain: whatever was opened, and whoever opened it, goes in one go. Each
	// step is allowed to fail, because any of them not being there is the state we are heading for.
	for _, step := range []string{
		fmt.Sprintf("iptables -D INPUT -i wlan0 -j %s", firewall.Chain),
		fmt.Sprintf("iptables -F %s", firewall.Chain),
		fmt.Sprintf("iptables -X %s", firewall.Chain),
	} {
		if _, _, err := r.d.ShellCode(step); err != nil {
			return "", false, err
		}
	}
	return layout.FirewallHook, false, nil
}
