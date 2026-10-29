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

const (
	API      = "ECHOLOCAL_API"
	ADB      = "ECHOLOCAL_ADB"
	Sendspin = "ECHOLOCAL_SENDSPIN"
	Web      = "ECHOLOCAL_WEB"
	Cast     = "ECHOLOCAL_CAST"
	AirPlay  = "ECHOLOCAL_AIRPLAY"
)

var All = []string{API, ADB, Sendspin, Web, Cast, AirPlay}

const iface = "wlan0"

var ready sync.Mutex

func acceptRule(chain, proto string, port int) []string {
	return []string{chain, "-i", iface, "-p", proto, "--dport", fmt.Sprint(port), "-j", "ACCEPT"}
}

func jumpRule(chain string) []string {
	return []string{"INPUT", "-i", iface, "-j", chain}
}

func Open(chain string, ports ...int) error {
	return Allow(chain, ports, nil)
}

func Allow(chain string, tcp, udp []int) error {
	ready.Lock()
	defer ready.Unlock()
	return open(chain, tcp, udp)
}

func open(chain string, tcp, udp []int) error {
	if err := ensure(chain); err != nil {
		return err
	}
	if err := iptablesRun("-F", chain); err != nil {
		return err
	}
	for proto, ports := range map[string][]int{"tcp": tcp, "udp": udp} {
		for _, port := range ports {
			if err := iptablesRun(
				append([]string{"-A"}, acceptRule(chain, proto, port)...)...); err != nil {
				return err
			}
		}
	}
	return nil
}

func Close(chain string) error {
	ready.Lock()
	defer ready.Unlock()

	if err := iptablesRun("-F", chain); err != nil && !errors.Is(err, errNoChain) {
		return err
	}
	return nil
}

func Opened(chain string, port int) (bool, error) {
	return has(acceptRule(chain, "tcp", port))
}

// firewall.sh flushes INPUT once the boot completes; the chains and their contents survive.
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

// INPUT ends in the vendor's own DROP.
func ensure(chain string) error {
	// -N fails when the chain already exists.
	if err := iptablesRun("-N", chain); err != nil && !errors.Is(err, errExists) {
		return err
	}

	there, err := has(jumpRule(chain))
	if err != nil || there {
		return err
	}
	return iptablesRun(append([]string{"-I"}, jumpRule(chain)...)...)
}

// iptables 1.4.20 takes -w without a timeout and then waits forever.
const lockWait = 5 * time.Second

func has(rule []string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()

	err := exec.CommandContext(ctx, iptables, append([]string{"-w", "-C"}, rule...)...).Run()
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, fmt.Errorf("firewall: -C %s: %w", strings.Join(rule, " "), ctx.Err())
	}

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return false, fmt.Errorf("firewall: %w", err)
}

// iptables reports these only in its message.
var (
	errExists  = errors.New("chain exists")
	errNoChain = errors.New("no such chain")
)

func iptablesRun(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()

	out, err := exec.CommandContext(ctx, iptables, append([]string{"-w"}, args...)...).
		CombinedOutput()
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
