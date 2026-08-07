// Package firewall opens ports in chains of our own.
//
// Amazon's firewall.sh sets the INPUT policy to DROP and allows only its own ports, so a listening
// service is unreachable until something adds a rule. Rules went straight into INPUT once, which
// worked but left nothing to take away: undoing them meant knowing every port anything had ever
// opened, and a rule nobody remembered adding stayed until the next reboot.
//
// So each thing that opens a port gets a chain named after itself, jumped to from INPUT. That is what
// lets the installer's hook and echod share the tables: each rebuilds only its own chain, where one
// shared chain meant whoever ran last flushed away the other's work.
//
// Closing empties the chain and leaves it, jump and all. An empty chain returns to INPUT and the
// vendor's DROP has the last word, so open and closed are the contents of one chain rather than rules
// coming and going from INPUT.
package firewall

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const iptables = "/system/bin/iptables"

// The chains, one per thing that opens a port. Uppercase because that is what every other tool's chain
// looks like in a listing, and somebody reading `iptables -L` should be able to tell at a glance both
// which rules are not the vendor's and what put them there.
const (
	API      = "ECHOLOCAL_API"
	ADB      = "ECHOLOCAL_ADB"
	Sendspin = "ECHOLOCAL_SENDSPIN"
)

var All = []string{API, ADB, Sendspin}

// The interface is wlan0 throughout: this device has no other way in.
const iface = "wlan0"

// ready serialises the read-then-write pairs below.
var ready sync.Mutex

func acceptRule(chain string, port int) []string {
	return []string{chain, "-i", iface, "-p", "tcp", "--dport", fmt.Sprint(port), "-j", "ACCEPT"}
}

func jumpRule(chain string) []string {
	return []string{"INPUT", "-i", iface, "-j", chain}
}

// Open allows a port. The chain is rebuilt rather than added to, so calling this twice leaves the same
// single rule.
func Open(chain string, port int) error {
	ready.Lock()
	defer ready.Unlock()
	return open(chain, port)
}

// open wants ready.
func open(chain string, port int) error {
	if err := ensure(chain); err != nil {
		return err
	}
	if err := iptablesRun("-F", chain); err != nil {
		return err
	}
	return iptablesRun(append([]string{"-A"}, acceptRule(chain, port)...)...)
}

// Close shuts the port by emptying the chain. The chain and its jump stay, so nothing has to be put
// back into INPUT ahead of the vendor's DROP the next time this opens.
func Close(chain string) error {
	ready.Lock()
	defer ready.Unlock()

	if err := iptablesRun("-F", chain); err != nil && !errors.Is(err, errNoChain) {
		return err
	}
	return nil
}

// Opened reports whether the port is allowed right now, which is the only honest answer after a
// restart: the process does not outlive its own rules, but they do outlive it.
func Opened(chain string, port int) (bool, error) {
	return has(acceptRule(chain, port))
}

// Ensure puts the jumps back, and only the jumps. firewall.sh flushes INPUT once the boot completes
// and takes them with it; the chains and their contents survive, so making them reachable again is the
// whole job. What is in a chain is its owner's business.
func Ensure() error {
	ready.Lock()
	defer ready.Unlock()

	for _, chain := range All {
		if err := ensure(chain); err != nil {
			return err
		}
	}
	return nil
}

// ensure creates the chain and the jump into it. Wants ready.
//
// The jump is inserted rather than appended: INPUT ends in the vendor's own DROP, and a rule after it
// is a rule that never runs.
func ensure(chain string) error {
	// -N fails when the chain is already there, which is the ordinary case and not an error.
	if err := iptablesRun("-N", chain); err != nil && !errors.Is(err, errExists) {
		return err
	}

	there, err := has(jumpRule(chain))
	if err != nil || there {
		return err
	}
	return iptablesRun(append([]string{"-I"}, jumpRule(chain)...)...)
}

// lockWait bounds how long to wait for whoever else is holding the tables. netd and firewall.sh both
// write them, and at boot we are in the middle of both. iptables 1.4.20 takes -w without a timeout and
// then waits forever, so the bound has to be ours.
const lockWait = 5 * time.Second

// has answers -C, where a non-zero exit is the answer rather than a failure.
func has(rule []string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()

	err := exec.CommandContext(ctx, iptables, append([]string{"-w", "-C"}, rule...)...).Run()
	if err == nil {
		return true, nil
	}
	// A kill for taking too long also exits non-zero, and that is not an answer about the rule.
	if ctx.Err() != nil {
		return false, fmt.Errorf("firewall: -C %s: %w", strings.Join(rule, " "), ctx.Err())
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
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()

	out, err := exec.CommandContext(ctx, iptables, append([]string{"-w"}, args...)...).CombinedOutput()
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
