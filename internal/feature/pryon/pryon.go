// Package pryon connects Amazon's native detector to EchoLocal's existing turn boundary.
// It owns no audio or conversation state: the Android companion reports a wake, and the selected
// Home Assistant slot decides which ordinary voice pipeline starts.
package pryon

import (
	"log/slog"
	"strings"

	"github.com/ygelfand/echolocal/internal/android/amazon"
	"github.com/ygelfand/echolocal/internal/feature/voice"
	"github.com/ygelfand/echolocal/internal/lib/wake"
)

func init() { amazon.Get().ListenWake(deliver) }

func deliver(event amazon.Wake) {
	if !strings.EqualFold(strings.TrimSpace(event.Phrase), "Alexa") {
		slog.Warn("Pryon wake ignored", "phrase", event.Phrase)
		return
	}

	v := voice.Get()
	slot := selectedSlot(v.ActiveWakeWords())
	if slot < 0 {
		slog.Info("Pryon wake ignored, Alexa is not selected", "phrase", event.Phrase)
		return
	}

	slog.Info("Pryon wake", "phrase", event.Phrase, "slot", slot+1,
		"confidence", event.Confidence)
	v.Start(slot)
}

func selectedSlot(active []string) int {
	for slot, id := range active {
		if id == wake.PryonID {
			return slot
		}
	}
	return -1
}
