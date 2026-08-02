package metrics

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// bootedAt and readAt are /proc/uptime read once, against the monotonic clock at that moment.
// Everything echod logs is stamped with the uptime, and reopening the file for each line is three
// syscalls to learn something the process can count itself.
var bootedAt, readAt = readUptime()

func readUptime() (float64, time.Time) {
	b, err := os.ReadFile("/proc/uptime")
	now := time.Now()
	if err != nil {
		return 0, now
	}
	first, _, _ := strings.Cut(string(b), " ")
	v, _ := strconv.ParseFloat(first, 64)
	return v, now
}

// Uptime is seconds since boot.
func Uptime() float64 { return bootedAt + time.Since(readAt).Seconds() }
