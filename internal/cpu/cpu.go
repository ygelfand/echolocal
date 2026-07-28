// Package cpu keeps the cores this device has available to echod.
package cpu

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// MediaTek's hotplug governor parks cores 2 and 3 within seconds of load dropping, and brings
// them back only after sustained load, which a wake word pipeline never produces on its own.
// Raising the floor pins them online and leaves the thermal and power limits it also enforces
// untouched.
const (
	floorPath   = "/proc/hps/num_base_perf_serv"
	presentPath = "/sys/devices/system/cpu/present"
)

// Unpark brings every present core online and makes the Go runtime use it. GOMAXPROCS has to be
// set explicitly: the runtime reads the affinity mask once at startup, when the cores this
// unparks were still offline.
func Unpark() (int, error) {
	n, err := present()
	if err != nil {
		return 0, err
	}
	if n < 2 {
		return n, nil
	}

	if err := os.WriteFile(floorPath, []byte(strconv.Itoa(n)), 0); err != nil {
		return runtime.GOMAXPROCS(0), fmt.Errorf("cpu: pinning %d cores online: %w", n, err)
	}
	runtime.GOMAXPROCS(n)
	return n, nil
}

func present() (int, error) {
	raw, err := os.ReadFile(presentPath)
	if err != nil {
		return 0, fmt.Errorf("cpu: reading present cores: %w", err)
	}
	return parsePresent(string(raw))
}

// parsePresent counts the cores the kernel knows about, from a range list such as "0-3".
func parsePresent(raw string) (int, error) {
	highest := -1
	for part := range strings.SplitSeq(strings.TrimSpace(raw), ",") {
		for bound := range strings.SplitSeq(part, "-") {
			n, err := strconv.Atoi(bound)
			if err != nil {
				return 0, fmt.Errorf("cpu: unexpected present cores %q", raw)
			}
			if n > highest {
				highest = n
			}
		}
	}
	if highest < 0 {
		return 0, fmt.Errorf("cpu: unexpected present cores %q", raw)
	}
	return highest + 1, nil
}
