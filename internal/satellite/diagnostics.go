package satellite

import (
	"fmt"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/speaker"
)

// WakeSlots is how many wake word slots Home Assistant offers, and so how many assistants there are
// to wake. It is fixed on its side: the ESPHome integration adds a pipeline and a wake word select
// at index 0 and 1 for every voice-capable device, whatever the device reports.
const WakeSlots = 2

// diagnostics are the entities that make the device do something on demand: to judge a setting by
// ear, or to reach a pipeline without saying anything to it.
type diagnostics struct {
	testPlayback *esphome.Button
	wake         []*esphome.Button
}

func newDiagnostics(k *kit, wake func(int)) *diagnostics {
	spk := k.Speaker
	d := &diagnostics{}

	if spk != nil {
		// A reply is 16 kHz audio fetched over HTTP and stretched here, so judging a resampling by
		// ear against a reply confounds the filter with the network and with whatever the pipeline
		// said. This is the same path with a known signal and nothing in front of it.
		d.testPlayback = &esphome.Button{
			Base: esphome.Base{
				ObjectID: "test_playback",
				Name:     "Test playback",
				Icon:     "mdi:waveform",
				Category: esphome.CategoryDiagnostic,
			},
			OnPress: func() {
				slog.Info("test playback", "resampling", spk.Resampling(), "step", spk.Step())
				spk.PlayVoice(speaker.VoiceSweep())
			},
		}
	}

	// Not diagnostic: waking the device by hand is something to do, not something to inspect.
	for slot := range WakeSlots {
		d.wake = append(d.wake, &esphome.Button{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("wake_assistant_%d", slot+1),
				Name:     fmt.Sprintf("Wake assistant %d", slot+1),
				Icon:     "mdi:account-voice",
			},
			OnPress: func() { wake(slot) },
		})
	}
	return d
}

func (d *diagnostics) entities() []esphome.Entity {
	var ents []esphome.Entity
	if d.testPlayback != nil {
		ents = append(ents, d.testPlayback)
	}
	for _, b := range d.wake {
		ents = append(ents, b)
	}
	return ents
}
