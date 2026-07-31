// Package firewall opens and closes a port on the running chain.
//
// Amazon's firewall.sh sets the INPUT policy to DROP and allows only its own ports, so a listening
// service is unreachable until something adds a rule. Nothing here writes a file: these rules last
// until the chain is rebuilt or the device reboots, which is the point for anything meant to be
// temporary.
package firewall

import (
	"fmt"
	"os/exec"
)

const iptables = "/system/bin/iptables"

func rule(port int) []string {
	return []string{"INPUT", "-i", "wlan0", "-p", "tcp", "--dport", fmt.Sprint(port), "-j", "ACCEPT"}
}

// Open adds the rule if it is not already there.
func Open(port int) error {
	if open, err := Opened(port); err != nil || open {
		return err
	}
	return run("-A", port)
}

// Close removes the rule if it is there.
func Close(port int) error {
	if open, err := Opened(port); err != nil || !open {
		return err
	}
	return run("-D", port)
}

// Opened reports whether the rule is in the chain now, which is the only honest answer after a restart:
// the process does not outlive its own rules, but they do outlive it.
func Opened(port int) (bool, error) {
	err := exec.Command(iptables, append([]string{"-C"}, rule(port)...)...).Run()
	if err == nil {
		return true, nil
	}
	if _, isExit := err.(*exec.ExitError); isExit {
		return false, nil
	}
	return false, fmt.Errorf("firewall: %w", err)
}

func run(op string, port int) error {
	out, err := exec.Command(iptables, append([]string{op}, rule(port)...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("firewall: iptables %s tcp/%d: %w: %s", op, port, err, out)
	}
	return nil
}
