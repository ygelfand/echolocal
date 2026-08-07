// Package firewall opens ports in a chain of our own.
//
// Amazon's firewall.sh sets the INPUT policy to DROP and allows only its own ports, so a listening
// service is unreachable until something adds a rule. Rules went straight into INPUT once, which
// worked but left nothing to take away: undoing them meant knowing every port anything had ever
// opened, and a rule nobody remembered adding stayed until the next reboot.
//
// So everything goes in one chain that INPUT jumps to. Opening a port is a rule in it, and giving the
// device back exactly as it was found is Teardown — one call, whatever was opened and by whom.
//
// Nothing here writes a file. These rules last until the chain is rebuilt or the device reboots, and
// the hook the installer leaves behind is what puts them back.
package firewall

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

const iptables = "/system/bin/iptables"

// Chain is ours. Uppercase because that is what every other tool's chain looks like in a listing, and
// somebody reading `iptables -L` should be able to tell at a glance which rules are not the vendor's.
const Chain = "ECHOLOCAL"

// The interface is wlan0 throughout: this device has no other way in.
const iface = "wlan0"

// ready guards the one-time chain creation. Opening two ports at once must not race to create it.
var ready sync.Mutex

func portRule(chain string, port int) []string {
	return []string{chain, "-i", iface, "-p", "tcp", "--dport", fmt.Sprint(port), "-j", "ACCEPT"}
}

func jumpRule() []string {
	return []string{"INPUT", "-i", iface, "-j", Chain}
}

// Ensure creates the chain and the jump into it, if they are not there already.
//
// The jump is inserted rather than appended: INPUT ends in the vendor's own DROP, and a rule after it
// is a rule that never runs.
func Ensure() error {
	ready.Lock()
	defer ready.Unlock()
	return ensure()
}

func ensure() error {
	// -N fails when the chain is already there, which is the ordinary case and not an error.
	if err := iptablesRun("-N", Chain); err != nil && !errors.Is(err, errExists) {
		return err
	}

	there, err := has(jumpRule())
	if err != nil || there {
		return err
	}
	return iptablesRun(append([]string{"-I"}, jumpRule()...)...)
}

// Open allows a port, creating the chain first if this is the first one.
func Open(port int) error {
	ready.Lock()
	defer ready.Unlock()

	if err := ensure(); err != nil {
		return err
	}

	open, err := has(portRule(Chain, port))
	if err != nil || open {
		return err
	}
	return iptablesRun(append([]string{"-A"}, portRule(Chain, port)...)...)
}

// Close removes a port, leaving the chain in place for whatever else is using it.
func Close(port int) error {
	ready.Lock()
	defer ready.Unlock()

	open, err := has(portRule(Chain, port))
	if err != nil || !open {
		return err
	}
	return iptablesRun(append([]string{"-D"}, portRule(Chain, port)...)...)
}

// Opened reports whether the port is allowed right now, which is the only honest answer after a
// restart: the process does not outlive its own rules, but they do outlive it.
func Opened(port int) (bool, error) {
	return has(portRule(Chain, port))
}

// Teardown removes the jump, empties the chain and deletes it, so nothing of ours is left in the
// tables. Safe to call when none of it is there.
func Teardown() error {
	ready.Lock()
	defer ready.Unlock()

	if there, err := has(jumpRule()); err != nil {
		return err
	} else if there {
		if err := iptablesRun(append([]string{"-D"}, jumpRule()...)...); err != nil {
			return err
		}
	}

	// Flushing before deleting because iptables will not delete a chain that still has rules in it.
	if err := iptablesRun("-F", Chain); err != nil && !errors.Is(err, errNoChain) {
		return err
	}
	if err := iptablesRun("-X", Chain); err != nil && !errors.Is(err, errNoChain) {
		return err
	}
	return nil
}

// has answers -C, where a non-zero exit is the answer rather than a failure.
func has(rule []string) (bool, error) {
	err := exec.Command(iptables, append([]string{"-C"}, rule...)...).Run()
	if err == nil {
		return true, nil
	}

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return false, fmt.Errorf("firewall: %w", err)
}

// The two failures worth telling apart from a real one, because both mean the tables are already in
// the state being asked for. iptables reports them only in its message.
var (
	errExists  = errors.New("chain exists")
	errNoChain = errors.New("no such chain")
)

func iptablesRun(args ...string) error {
	out, err := exec.Command(iptables, args...).CombinedOutput()
	if err == nil {
		return nil
	}

	switch said := string(out); {
	case strings.Contains(said, "Chain already exists"):
		return errExists
	case strings.Contains(said, "No chain/target/match by that name"):
		return errNoChain
	}
	return fmt.Errorf("firewall: iptables %s: %w: %s", strings.Join(args, " "), err, out)
}
