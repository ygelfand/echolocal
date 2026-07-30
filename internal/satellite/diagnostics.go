package satellite

import (
	"fmt"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/echolocal/internal/layout"
	"github.com/ygelfand/echolocal/internal/settings"
	"github.com/ygelfand/echolocal/internal/speaker"
	"github.com/ygelfand/echolocal/internal/wake"
)

// WakeSlots is how many wake word slots Home Assistant offers, and so how many assistants there are
// to wake. It is fixed on its side: the ESPHome integration adds a pipeline and a wake word select
// at index 0 and 1 for every voice-capable device, whatever the device reports.
const WakeSlots = 2

// diagnostics are the entities that make the device do something on demand, or say something about
// itself: to judge a setting by ear, to reach a pipeline without saying anything to it, or to see
// what is filling up the disk.
type diagnostics struct {
	testPlayback *esphome.Button
	wake         []*esphome.Button

	cached *esphome.Sensor
	free   *esphome.Sensor
	purge  *esphome.Button
}

func newDiagnostics(k *kit, wake func(int)) *diagnostics {
	spk := k.Speaker
	d := &diagnostics{}

	d.storage()

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

// storage builds what the device says about its own disk. Cached is what could be deleted without
// losing anything: wake word models no slot is listening for, which Home Assistant still offers and
// will serve again on the next selection. Nothing else is cache yet.
//
// Reported in kilobytes because the protocol carries a state as a float32, which counts bytes exactly
// only to sixteen megabytes. Home Assistant converts for display, so a size class in kB can still be
// read in MB.
func (d *diagnostics) storage() {
	d.cached = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "cached_data",
			Name:     "Cached data",
			Icon:     "mdi:cached",
			Category: esphome.CategoryDiagnostic,
		},
		Unit:        "kB",
		DeviceClass: "data_size",
		StateClass:  esphome.StateClassMeasurement,
	}
	d.free = &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "free_space",
			Name:     "Free space",
			Icon:     "mdi:harddisk",
			Category: esphome.CategoryDiagnostic,
		},
		Unit:        "kB",
		DeviceClass: "data_size",
		StateClass:  esphome.StateClassMeasurement,
	}

	d.purge = &esphome.Button{
		Base: esphome.Base{
			ObjectID: "purge_cache",
			Name:     "Purge cache",
			Icon:     "mdi:delete-sweep",
			Category: esphome.CategoryDiagnostic,
		},
		OnPress: func() {
			gone, freed := wake.Purge(layout.ModelDir, inUseWakeWords())
			slog.Info("cache purged", "models", gone, "bytes", freed)
			d.measure()
		},
	}
}

// measure republishes what the disk holds. It runs at start-up and after anything that adds to the
// cache or takes from it, rather than on a timer: nothing here changes on its own.
func (d *diagnostics) measure() {
	if d.cached == nil {
		return
	}

	_, cached := wake.Cached(layout.ModelDir, inUseWakeWords())
	d.cached.Set(float32(cached / 1024))

	free, err := layout.Free(layout.StateDir)
	if err != nil {
		slog.Error("reading free space failed", "dir", layout.StateDir, "err", err)
		return
	}
	d.free.Set(float32(free / 1024))
}

// inUseWakeWords is what the slots are set to, which is what a purge has to leave alone.
func inUseWakeWords() []string {
	saved := settings.Get().Wake

	var ids []string
	for slot := range WakeSlots {
		if id := saved.WordID(slot); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (d *diagnostics) entities() []esphome.Entity {
	ents := []esphome.Entity{d.cached, d.free, d.purge}
	if d.testPlayback != nil {
		ents = append(ents, d.testPlayback)
	}
	for _, b := range d.wake {
		ents = append(ents, b)
	}
	return ents
}
