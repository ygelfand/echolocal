package boot

import (
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
)

func procs() {
	have := present()
	if have <= runtime.GOMAXPROCS(0) {
		return
	}
	slog.Info("processors", "was", runtime.GOMAXPROCS(have), "now", have, "online", runtime.NumCPU())
}

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
