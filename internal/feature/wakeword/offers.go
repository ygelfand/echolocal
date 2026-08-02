package wakeword

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/lib/wake"
)

// Answer is what the device says when Home Assistant asks what it can hear. The question carries the
// models Home Assistant hosts in its own custom_wake_words directory, each with the URL to fetch it
// from, so the answer is that set combined with what is on disk.
//
// It is asked on connect and again after every selection change, which is why nothing about the answer
// is kept: it is worked out when the question arrives. The offers themselves are remembered only
// because a selection names an id and not a URL.
func Answer(offered []esphome.ExternalWakeWord) []esphome.WakeWord {
	lib := wake.Lib()

	lib.Offered(offered)
	words, shadowed := lib.Advertise()

	slog.Info("wake words offered", "count", len(offered),
		"ours", len(lib.Ours()), "advertised", len(words), "shadowed", shadowed)
	return words
}
