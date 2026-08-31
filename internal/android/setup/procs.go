package setup

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// procs tells the Go runtime how many processors there really are. The kernel hotplugs them, so the
// number online when the runtime started is whatever the governor happened to be running, and
// GOMAXPROCS is only read once.
func procs() error {
	have := Present()
	if have <= runtime.GOMAXPROCS(0) {
		return nil
	}

	was := runtime.GOMAXPROCS(have)
	slog.Info("processors", "was", was, "now", have, "online", runtime.NumCPU())
	return nil
}

// MinCores holds at least n cores online.
func MinCores(n int) error {
	if n < 1 || n > Present() {
		return fmt.Errorf("setup: %d cores is outside 1..%d", n, Present())
	}
	return os.WriteFile("/proc/hps/num_base_perf_serv", []byte(strconv.Itoa(n)), 0o644)
}

// Present counts what /sys says exists, online or not. The format is a list of numbers and ranges.
func Present() int {
	data, err := os.ReadFile("/sys/devices/system/cpu/present")
	if err != nil {
		return 0
	}

	n := 0
	for part := range strings.SplitSeq(strings.TrimSpace(string(data)), ",") {
		lo, hi, ranged := strings.Cut(part, "-")
		first, err := strconv.Atoi(lo)
		if err != nil {
			return 0
		}
		if !ranged {
			n++
			continue
		}
		last, err := strconv.Atoi(hi)
		if err != nil {
			return 0
		}
		n += last - first + 1
	}
	return n
}
