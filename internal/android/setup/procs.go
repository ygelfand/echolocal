package setup

import (
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
	have := present()
	if have <= runtime.GOMAXPROCS(0) {
		return nil
	}

	was := runtime.GOMAXPROCS(have)
	slog.Info("processors", "was", was, "now", have, "online", runtime.NumCPU())
	return nil
}

// present counts what /sys says exists, online or not. The format is a list of numbers and ranges.
func present() int {
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
